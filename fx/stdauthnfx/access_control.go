package stdauthnfx

import (
	"context"
	"encoding/base64"
	"time"

	"buf.build/go/protovalidate"
	"github.com/advdv/stdgo/stdfx"
	"github.com/cockroachdb/errors"
	"github.com/lestrrat-go/httprc/v3"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"go.uber.org/fx"
)

type Config struct {
	// The base64-encoded key information for signing.
	SigningKeySetBase64 string `env:"SIGNING_KEY_SET_BASE64,required"`
	// SigningKeyID is the id we use for signing
	SigningKeyID string `env:"SIGNING_KEY_ID,required"`
	// Access Token validation JWKS endpoint
	TokenValidationJWKSEndpoint string `env:"TOKEN_VALIDATION_JWKS_ENDPOINT,required"`
	// Access Token issuer to be checked.
	TokenIssuer string `env:"TOKEN_ISSUER,required"`
	// Access Token audience to be checked.
	TokenAudience string `env:"TOKEN_AUDIENCE,required"`
	// Configure a fixed wall-clock time as far as token validation is concerned. Only useful in testing.
	FixedWallClockTimestamp int64 `env:"FIXED_WALL_CLOCK_TIMESTAMP"`
}

type AccessControl struct {
	config    Config
	validator protovalidate.Validator

	// api key signing and validation
	apiKeys struct {
		signingKeySet jwk.Set
		signingKeyID  string
		signingKey    jwk.Key
	}

	// auth provider access tokens
	accessTokens struct {
		cache *jwk.Cache
		clock jwt.Clock
	}
}

func New(deps struct {
	fx.In
	Config    Config
	Validator protovalidate.Validator
},
) (res struct {
	fx.Out
	AccessControl *AccessControl
}, err error,
) {
	cfg := deps.Config
	res.AccessControl = &AccessControl{config: cfg, validator: deps.Validator}

	// setup keys for api key signing and validation.
	{
		keySetData, err := base64.StdEncoding.DecodeString(cfg.SigningKeySetBase64)
		if err != nil {
			return res, errors.Errorf("decode keyset (base64): %w", err)
		}

		signingSet, err := jwk.Parse(keySetData)
		if err != nil {
			return res, errors.Errorf("parse keyset: %w", err)
		}

		res.AccessControl.apiKeys.signingKeySet = signingSet
		res.AccessControl.apiKeys.signingKeyID = cfg.SigningKeyID

		var ok bool
		if res.AccessControl.apiKeys.signingKey, ok = signingSet.LookupKeyID(cfg.SigningKeyID); !ok {
			return res, errors.Errorf("configured key id '%s' not in key set", cfg.SigningKeyID)
		}
	}

	// setup keys for validating access tokens from our auth provider.
	{
		if cfg.FixedWallClockTimestamp == 0 {
			res.AccessControl.accessTokens.clock = jwt.ClockFunc(time.Now)
		} else {
			res.AccessControl.accessTokens.clock = jwt.ClockFunc(func() time.Time {
				return time.Unix(cfg.FixedWallClockTimestamp, 0)
			})
		}

		cacheLifecycleCtx := context.Background()
		res.AccessControl.accessTokens.cache, err = jwk.NewCache(cacheLifecycleCtx, httprc.NewClient())
		if err != nil {
			return res, errors.Errorf("init jwk cache for access tokens: %w", err)
		}

		registerCtx, cancel := context.WithTimeout(cacheLifecycleCtx, time.Second*3)
		defer cancel()
		if err = res.AccessControl.accessTokens.cache.Register(registerCtx, cfg.TokenValidationJWKSEndpoint); err != nil {
			return res, errors.Errorf("register jwk resource: %w", err)
		}
	}

	return res, nil
}

func Provide() fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdauthn", New)
}
