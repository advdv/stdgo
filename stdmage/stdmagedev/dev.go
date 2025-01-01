// Package stdmagedev provides re-usable project scripts.
package stdmagedev

import (
	"fmt"

	"github.com/advdv/stdgo/stdmage"
	"github.com/magefile/mage/sh"
)

// Init inits the mage targets.
func Init() {
	stdmage.MustBeInRootIfNotTest()
}

// Lint our codebase.
func Lint() error {
	if err := sh.Run("golangci-lint", "run"); err != nil {
		return fmt.Errorf("failed to run golang-ci: %w", err)
	}

	return nil
}

// Test tests the whole codebase.
func Test() error {
	if err := sh.Run("go", "test", "./..."); err != nil {
		return fmt.Errorf("failed to run tests: %w", err)
	}

	return nil
}

// Generate generates code.
func Generate() error {
	if err := sh.Run("go", "generate", "./..."); err != nil {
		return fmt.Errorf("failed to run tests: %w", err)
	}

	return nil
}

// Serve the code locally.
func Serve() error {
	if err := stdmage.LoadEnv("dev"); err != nil {
		return fmt.Errorf("failed to load development env: %w", err)
	}

	if err := sh.RunWith(map[string]string{}, "docker", "compose",
		"-f", "docker-compose.yml",
		"up",
		"-d", "--build", "--remove-orphans", "--force-recreate",
	); err != nil {
		return fmt.Errorf("failed to run: %w", err)
	}

	return nil
}
