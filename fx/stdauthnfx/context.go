package stdauthnfx

import "context"

type ctxKey string

func WithIdentity(ctx context.Context, idn Identity) context.Context {
	return context.WithValue(ctx, ctxKey("identity"), idn)
}

func IdentityFromContext(ctx context.Context) Identity {
	idn, ok := ctx.Value(ctxKey("identity")).(Identity)
	if !ok {
		panic("stdauthnfx: no identity in context")
	}

	return idn
}
