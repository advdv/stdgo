package stdpubprivrpcfx

import "go.uber.org/fx"

type BasePath struct{ V string }

func ProvideBasePath(base string) fx.Option {
	return fx.Supply(BasePath{base})
}
