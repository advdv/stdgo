package stdauthnfx

import (
	"context"
	"encoding/base64"
	"strings"

	stdauthnfxv1 "github.com/advdv/stdgo/fx/stdauthnfx/v1"
	"github.com/cockroachdb/errors"
	"github.com/deatil/go-encoding/base62"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"google.golang.org/protobuf/proto"
)

const (
	// prefix to recognize our API keys.
	APIKeyPrefix = "bwak_"
)

// BuildAndSignAPIKey takes an access and signs it as our API keys.
func (ac *AccessControl) BuildAndSignAPIKey(acc *stdauthnfxv1.Access) (string, error) {
	if err := ac.validator.Validate(acc); err != nil {
		return "", errors.Errorf("invalid access for build-and-sign: %w", err)
	}

	bldr := jwt.NewBuilder()

	tok, err := bldr.Build()
	if err != nil {
		return "", errors.Errorf("build jwt: %w", err)
	}

	data, err := proto.Marshal(acc)
	if err != nil {
		return "", errors.Errorf("marshal access: %w", err)
	}

	if err := tok.Set("access", base64.StdEncoding.EncodeToString(data)); err != nil {
		return "", errors.Errorf("set access data on token: %w", err)
	}

	algo, ok := ac.apiKeys.signingKey.Algorithm()
	if !ok {
		return "", errors.Errorf("key without algorithm: %v", ac.apiKeys.signingKey)
	}

	signed, err := jwt.Sign(tok, jwt.WithKey(algo, ac.apiKeys.signingKey))
	if err != nil {
		return "", errors.Errorf("signing: %w", err)
	}

	return APIKeyPrefix + base62.StdEncoding.EncodeToString(signed), nil
}

func (ac *AccessControl) authenticateAPIKey(ctx context.Context, apiKey string) (context.Context, error) {
	noSuffixAPIKey := strings.TrimPrefix(apiKey, APIKeyPrefix)
	apiKeyb, err := base62.StdEncoding.DecodeString(noSuffixAPIKey)
	if err != nil {
		return ctx, errors.Errorf("decode api key: %w", err)
	}

	opts := []jwt.ParseOption{
		jwt.WithKeySet(ac.apiKeys.signingKeySet), // from our own signing set
		jwt.WithValidate(true),                   // on by default, but let's be explicit about this
	}

	tok, err := jwt.ParseString(string(apiKeyb), opts...)
	if err != nil {
		return ctx, errors.Errorf("invalid token: %w", err)
	}

	var b64Data string
	if err := tok.Get("access", &b64Data); err != nil {
		return ctx, errors.Errorf("get access data from token: %w", err)
	}

	data, err := base64.StdEncoding.DecodeString(b64Data)
	if err != nil {
		return ctx, errors.Errorf("base64-decode access data: %w", err)
	}

	var acc stdauthnfxv1.Access
	if err := proto.Unmarshal(data, &acc); err != nil {
		return ctx, errors.Errorf("unmarshal access: %w", err)
	}

	ctx = WithAPIKeyFingerprint(ctx, ac.hasher(), []byte(apiKey))

	return WithAccess(ctx, ac.validator, &acc), nil
}
