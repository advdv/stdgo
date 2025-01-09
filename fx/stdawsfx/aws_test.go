package stdawsfx_test

import (
	"testing"

	"github.com/advdv/stdgo/fx/stdawsfx"
	"github.com/aws/aws-sdk-go-v2/aws"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func TestNew(t *testing.T) {
	var acfg aws.Config
	app := fxtest.New(t, stdawsfx.Provide(), fx.Populate(&acfg))
	app.RequireStart()
	t.Cleanup(app.RequireStop)
}
