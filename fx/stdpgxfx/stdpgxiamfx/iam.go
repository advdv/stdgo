// Package stdpgxiamfx provides RDS IAM authentication for stdpgxfx connection pools.
//
// It provides a stdpgxfx.IamTokenFunc that builds a fresh RDS IAM auth token for
// every new connection. It requires an aws.Config in the fx graph (e.g., from
// stdawsfx.Provide()) and only takes effect when STDPGX_IAM_AUTH is enabled.
//
// The signing region is derived per-connection from the RDS hostname (e.g.,
// "mycluster.cluster-xxx.eu-central-1.rds.amazonaws.com" → "eu-central-1").
// This means a derived pool that points at a different regional endpoint (e.g.,
// a reader endpoint in another region, possibly set through STDPGX_HOSTS) signs
// tokens against that region automatically. The hostname must match the
// standard regional RDS pattern; non-RDS hostnames (e.g., custom domains, RDS
// Proxy aliases, the global writer endpoint) are not supported.
package stdpgxiamfx

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/advdv/stdgo/fx/stdpgxfx"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/rds/auth"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// rdsRegionalHostRE matches regional Aurora/RDS endpoints and captures the region.
// Examples that match:
//
//	mycluster.cluster-abc123.eu-central-1.rds.amazonaws.com         → eu-central-1
//	mycluster.cluster-ro-abc123.eu-central-1.rds.amazonaws.com      → eu-central-1
//	instance-1.abc123.ap-southeast-1.rds.amazonaws.com              → ap-southeast-1
//
// Examples that DO NOT match — IAM auth is unsupported for these:
//
//	mycluster.global-abc123.global.rds.amazonaws.com                (global writer endpoint, no region)
//	localhost                                                       (local dev)
//	myproxy.example.com                                             (custom domain / RDS Proxy alias)
var rdsRegionalHostRE = regexp.MustCompile(
	`^[^.]+\.[^.]+\.(?P<region>[a-z]{2}-[a-z]+-\d+)\.rds\.amazonaws\.com\.?$`)

// DeriveSigningRegion attempts to extract the AWS region from the given host
// using the standard RDS regional hostname format. Returns an empty string if
// the host doesn't match the regional pattern.
func DeriveSigningRegion(host string) string {
	m := rdsRegionalHostRE.FindStringSubmatch(host)
	if m == nil {
		return ""
	}
	return m[rdsRegionalHostRE.SubexpIndex("region")]
}

// New constructs the IAM token func that stdpgxfx uses when IAM auth is enabled.
func New(awsCfg aws.Config, logs *zap.Logger) (stdpgxfx.IamTokenFunc, error) {
	if awsCfg.Credentials == nil {
		return nil, errors.New("IAM authentication requires an AWS configuration with credentials")
	}

	return func(ctx context.Context, pgc *pgx.ConnConfig) (string, error) {
		region := DeriveSigningRegion(pgc.Host)
		if region == "" {
			return "", fmt.Errorf("could not derive IAM signing region from host %q: "+
				"hostname must match a regional RDS pattern (*.<region>.rds.amazonaws.com)", pgc.Host)
		}

		return buildIamAuthToken(ctx, logs, pgc.Port, pgc.User, region, awsCfg, pgc.Host)
	}, nil
}

// buildIamAuthToken will construct a RDS authentication token. We don't run this during the
// lifecycle phase so we timeout manually with our own context.
func buildIamAuthToken(
	ctx context.Context,
	logs *zap.Logger,
	port uint16,
	username, region string,
	awsc aws.Config,
	host string,
) (string, error) {
	ep := host + ":" + fmt.Sprintf("%d", port)

	logs.Debug("building IAM auth token",
		zap.String("username", username),
		zap.String("region", region),
		zap.String("ep", ep))

	tok, err := auth.BuildAuthToken(ctx, ep, region, username, awsc.Credentials)
	if err != nil {
		return "", fmt.Errorf("underlying: %w", err)
	}

	return tok, nil
}

// Provide the IAM token func as an fx dependency for stdpgxfx.
func Provide() fx.Option {
	return fx.Module("stdpgxiam", fx.Provide(New))
}
