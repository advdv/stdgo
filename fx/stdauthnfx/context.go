package stdauthnfx

import (
	"context"
	"hash"

	"buf.build/go/protovalidate"
	stdauthnfxv1 "github.com/advdv/stdgo/fx/stdauthnfx/v1"
	"google.golang.org/protobuf/proto"
)

type ctxKey string

func WithAccess(ctx context.Context, val protovalidate.Validator, access *stdauthnfxv1.Access) context.Context {
	if err := val.Validate(access); err != nil {
		panic("stdauthnfx: invalid access for context: " + err.Error())
	}

	return context.WithValue(ctx, ctxKey("access"), access)
}

func WithAnonymousAccess(ctx context.Context, val protovalidate.Validator) context.Context {
	return WithAccess(ctx, val, stdauthnfxv1.Access_builder{
		IsAnonymous: proto.Bool(true),
	}.Build())
}

func WithWebUserAccess(
	ctx context.Context, val protovalidate.Validator, info *stdauthnfxv1.AccessIdentity,
) context.Context {
	return WithAccess(ctx, val, stdauthnfxv1.Access_builder{
		WebuserIdentity: info,
	}.Build())
}

func FromContext(ctx context.Context) *stdauthnfxv1.Access {
	v, ok := ctx.Value(ctxKey("access")).(*stdauthnfxv1.Access)
	if !ok {
		panic("stdauthnfx: no access information in context")
	}

	return v
}

func WithAPIKeyFingerprint(ctx context.Context, hash hash.Hash, data []byte) context.Context {
	if _, err := hash.Write(data); err != nil {
		panic("stdauthnfx: hash api key: " + err.Error())
	}

	return context.WithValue(ctx, ctxKey("api_key_fingerprint"), hash.Sum(nil))
}

func APIKeyFingerprint(ctx context.Context) ([]byte, bool) {
	v, ok := ctx.Value(ctxKey("api_key_fingerprint")).([]byte)
	return v, ok
}
