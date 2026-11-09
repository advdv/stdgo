// Package stds3fx provides an S3 client (minio-go) and the buckets it may
// use, resolved from aliases so code never spells out a deployment's bucket
// names. The client speaks the S3 API to STDS3_ENDPOINT, which is AWS S3 by
// default and any S3-compatible server otherwise. With STDS3_ACCESS_KEY and
// STDS3_SECRET_KEY it signs with those; without them it finds credentials
// the way AWS SDKs do, from the environment, the shared credentials file, or
// the role of the EC2 instance, ECS task or Kubernetes service account.
//
// Pre-signed requests are signed for STDS3_PUBLIC_ENDPOINT when it is set, for
// clients that reach the server at another address than the service does, such
// as a browser on the host of a Docker Compose network the service runs in.
//
// Tests get isolated buckets the way they get isolated databases: with
// STDS3_CREATE_AND_USE_RANDOM_BUCKETS every configured bucket name gets a
// random suffix and is created when the fx application starts, and with
// STDS3_AUTO_REMOVE_BUCKETS it is emptied and deleted when it stops.
// stds3testfx sets both. Leave them unset elsewhere, where infrastructure
// owns the buckets.
package stds3fx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"maps"
	"net/url"
	"slices"

	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/cockroachdb/errors"
	"github.com/destel/rill"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Config is the env-driven configuration of the module (prefix STDS3_).
type Config struct {
	// Buckets maps every bucket alias the code uses to the bucket name of the
	// environment, as alias:name pairs separated by commas.
	Buckets map[string]string `env:"BUCKETS,required"`
	// Endpoint is the URL of the S3 API, of AWS or an S3-compatible server. Its scheme
	// decides whether the client uses TLS.
	Endpoint string `env:"ENDPOINT" envDefault:"https://s3.amazonaws.com"`
	// PublicEndpoint is the URL clients send pre-signed requests to, when it is
	// not Endpoint. A signature covers the host, so a request signed for one
	// address fails at another. Leave it unset where both are the same.
	PublicEndpoint string `env:"PUBLIC_ENDPOINT"`
	// Region is the region the client signs for. Setting it spares the client
	// a bucket location lookup; most S3-compatible servers accept any region.
	Region string `env:"REGION" envDefault:"us-east-1"`
	// AccessKey and SecretKey are static credentials, such as those of an
	// S3-compatible server. Without them the client uses the AWS credential
	// sources.
	AccessKey string `env:"ACCESS_KEY"`
	SecretKey string `env:"SECRET_KEY"`
	// CreateAndUseRandomBuckets suffixes every bucket name with a random hex
	// string and creates the buckets on start, so parallel tests against one
	// server do not see each other's objects.
	CreateAndUseRandomBuckets bool `env:"CREATE_AND_USE_RANDOM_BUCKETS"`
	// AutoRemoveBuckets empties and deletes the random buckets on stop.
	AutoRemoveBuckets bool `env:"AUTO_REMOVE_BUCKETS"`
}

// Client is the object storage client: the S3 client plus the resolved bucket
// names. It is safe for concurrent use.
type Client struct {
	cfg     Config
	logs    *zap.Logger
	s3      *minio.Client
	signer  *minio.Client
	buckets map[string]string
}

// Params is the fx input for [New].
type Params struct {
	fx.In
	fx.Lifecycle

	Config Config
	Logs   *zap.Logger
}

// New builds the client and, in random-bucket mode, ties bucket creation and
// removal to the application lifecycle.
func New(params Params) (*Client, error) {
	cfg := params.Config
	if len(cfg.Buckets) == 0 {
		return nil, errors.New("STDS3_BUCKETS must declare at least one alias:name pair")
	}

	if (cfg.AccessKey == "") != (cfg.SecretKey == "") {
		return nil, errors.New("STDS3_ACCESS_KEY and STDS3_SECRET_KEY must be set together")
	}

	buckets, err := resolveBuckets(cfg)
	if err != nil {
		return nil, errors.Wrap(err, "resolve bucket names")
	}

	creds := newCredentials(cfg)

	s3, err := newS3Client(cfg.Endpoint, cfg.Region, creds)
	if err != nil {
		return nil, errors.Wrap(err, "STDS3_ENDPOINT")
	}

	// The signer only signs, which needs no request since the region is set.
	signer := s3
	if cfg.PublicEndpoint != "" {
		if signer, err = newS3Client(cfg.PublicEndpoint, cfg.Region, creds); err != nil {
			return nil, errors.Wrap(err, "STDS3_PUBLIC_ENDPOINT")
		}
	}

	client := &Client{cfg: cfg, logs: params.Logs, s3: s3, signer: signer, buckets: buckets}

	params.Logs.Info("s3 client configured",
		zap.Strings("bucket_aliases", slices.Sorted(maps.Keys(buckets))),
		zap.String("endpoint", cfg.Endpoint),
		zap.String("public_endpoint", cfg.PublicEndpoint),
		zap.Bool("static_credentials", cfg.AccessKey != ""),
		zap.Bool("random_buckets", cfg.CreateAndUseRandomBuckets))

	params.Append(fx.Hook{OnStart: client.start, OnStop: client.stop})

	return client, nil
}

// S3 returns the underlying S3 client for object operations; callers get
// bucket names from [Client.Bucket].
func (c *Client) S3() *minio.Client { return c.s3 }

// Bucket returns the bucket name for an alias, such as "main". An alias
// that is not configured returns ok=false, which callers must treat as a
// programming error rather than fall back to another bucket.
func (c *Client) Bucket(alias string) (string, bool) {
	name, ok := c.buckets[alias]

	return name, ok
}

// Ping reports whether every configured bucket is reachable and exists, with a
// HeadBucket per bucket, all at once. It returns the first failure and stops
// the other checks. It only reads, so it is safe for the health check.
func (c *Client) Ping(ctx context.Context) error {
	aliases := slices.Sorted(maps.Keys(c.buckets))

	err := rill.ForEach(rill.FromSlice(aliases, nil), len(aliases), func(alias string) error {
		exists, err := c.s3.BucketExists(ctx, c.buckets[alias])
		if err != nil {
			return errors.Wrapf(err, "head bucket %q", alias)
		}

		if !exists {
			return errors.Errorf("head bucket %q: bucket does not exist", alias)
		}

		return nil
	})

	return errors.Wrap(err, "ping buckets")
}

// start creates the random buckets. Outside random-bucket mode it does nothing:
// infrastructure owns the buckets.
func (c *Client) start(ctx context.Context) error {
	if !c.cfg.CreateAndUseRandomBuckets {
		return nil
	}

	for alias, bucket := range c.buckets {
		err := c.s3.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: c.cfg.Region})
		if err != nil && !isBucketAlreadyOwned(err) {
			return errors.Wrapf(err, "create random bucket for alias %q", alias)
		}
	}

	c.logs.Info("created random buckets", zap.Int("count", len(c.buckets)))

	return nil
}

// stop empties and deletes the random buckets. A bucket that is already gone,
// because a test deleted it, counts as removed.
func (c *Client) stop(ctx context.Context) error {
	if !c.cfg.CreateAndUseRandomBuckets || !c.cfg.AutoRemoveBuckets {
		return nil
	}

	for alias, bucket := range c.buckets {
		if err := c.clearBucket(ctx, bucket); err != nil {
			return errors.Wrapf(err, "clear random bucket for alias %q", alias)
		}

		if err := c.s3.RemoveBucket(ctx, bucket); err != nil && !isNoSuchBucket(err) {
			return errors.Wrapf(err, "delete random bucket for alias %q", alias)
		}
	}

	return nil
}

// clearBucket deletes every object version and delete marker in a bucket with
// bulk deletes. Listing versions instead of objects also empties a versioned
// bucket, which plain deletes would only fill with delete markers; on an
// unversioned bucket it lists the objects.
func (c *Client) clearBucket(ctx context.Context, bucket string) error {
	var listErr error

	versions := func(yield func(minio.ObjectInfo) bool) {
		opts := minio.ListObjectsOptions{Recursive: true, WithVersions: true}
		for object := range c.s3.ListObjectsIter(ctx, bucket, opts) {
			if object.Err != nil {
				listErr = object.Err

				return
			}

			if !yield(object) {
				return
			}
		}
	}

	results, err := c.s3.RemoveObjectsWithIter(ctx, bucket, versions, minio.RemoveObjectsOptions{})
	if err != nil {
		return errors.Wrap(err, "delete object versions")
	}

	for result := range results {
		if result.Err != nil {
			return errors.Wrapf(result.Err, "delete object %q version %q", result.ObjectName, result.ObjectVersionID)
		}
	}

	if listErr != nil && !isNoSuchBucket(listErr) {
		return errors.Wrap(listErr, "list object versions")
	}

	return nil
}

// newCredentials returns the static credentials when configured, and otherwise
// the AWS chain: environment variables, the shared credentials file, then the
// role of the instance, task or service account.
func newCredentials(cfg Config) *credentials.Credentials {
	if cfg.AccessKey != "" {
		return credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, "")
	}

	return credentials.NewChainCredentials([]credentials.Provider{
		&credentials.EnvAWS{},
		&credentials.FileAWSCredentials{},
		&credentials.IAM{},
	})
}

// newS3Client builds a client for the S3 API at endpoint, a URL whose scheme
// selects TLS. It addresses buckets by path except on AWS, which it addresses
// by virtual host.
func newS3Client(endpoint, region string, creds *credentials.Credentials) (*minio.Client, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, errors.Wrap(err, "parse endpoint")
	}

	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.Errorf("endpoint %q must be an http(s) URL without a path", endpoint)
	}

	client, err := minio.New(parsed.Host, &minio.Options{
		Creds:  creds,
		Secure: parsed.Scheme == "https",
		Region: region,
	})
	if err != nil {
		return nil, errors.Wrap(err, "new s3 client")
	}

	return client, nil
}

// resolveBuckets returns the alias to bucket name map the client uses: a copy
// of the configured one, with one random suffix shared by every bucket in
// random-bucket mode so one application owns one cohort of buckets.
func resolveBuckets(cfg Config) (map[string]string, error) {
	buckets := maps.Clone(cfg.Buckets)
	if !cfg.CreateAndUseRandomBuckets {
		return buckets, nil
	}

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return nil, errors.Wrap(err, "random bucket suffix")
	}

	for alias, name := range buckets {
		buckets[alias] = name + "-" + hex.EncodeToString(suffix)
	}

	return buckets, nil
}

// isBucketAlreadyOwned reports the MakeBucket errors that mean the bucket
// exists, which makes a retried start idempotent.
func isBucketAlreadyOwned(err error) bool {
	return hasErrorCode(err, minio.BucketAlreadyOwnedByYou, minio.BucketAlreadyExists)
}

// isNoSuchBucket reports the errors that mean the bucket is already gone.
func isNoSuchBucket(err error) bool {
	return hasErrorCode(err, minio.NoSuchBucket)
}

// hasErrorCode matches S3 errors by their API error code. Unlike
// minio.ToErrorResponse, it also finds the error response behind wrapping.
func hasErrorCode(err error, codes ...string) bool {
	var response minio.ErrorResponse
	if !errors.As(err, &response) {
		return false
	}

	return slices.Contains(codes, response.Code)
}

// Provide wires the module. It spells out what stdfx.ZapEnvCfgModule does,
// whose prefix for "stds3" would be STDS_3_.
func Provide() fx.Option {
	return fx.Module("stds3",
		stdenvcfg.Provide[Config]("STDS3_"),
		fx.Decorate(func(l *zap.Logger) *zap.Logger { return l.Named("stds3") }),
		fx.Provide(New),
	)
}
