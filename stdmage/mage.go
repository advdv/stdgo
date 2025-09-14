// Package stdmage provides utility methods that are shared between mage targets.
package stdmage

import (
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// MustBeInRootIfNotTest should be run in an init to make sure we run mage targets always from the
// root of a go project.
func MustBeInRootIfNotTest() {
	const filename = "go.mod"

	if _, err := os.ReadFile(filename); err != nil && !strings.Contains(strings.Join(os.Args, ""), "-test.") {
		panic("must be in project root, couldn't stat " + filename + " file: " + err.Error())
	}
}

// LoadEnv for loading environment variables for standard dev, stag and prod.
func LoadEnv(env string) error {
	if err := godotenv.Overload(".env", ".env."+env); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to load env variables: %w", err)
	}

	return nil
}
