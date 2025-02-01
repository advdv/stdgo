// Package stdmagedev provides re-usable project scripts.
package stdmagedev

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/advdv/stdgo/stdmage"
	"github.com/magefile/mage/sh"
)

var devEnv = "dev"

// Init inits the mage targets. The weird signature is to make Mage ignore this when importing.
func Init(dotEnvDevEnv string, _ ...[]string) {
	devEnv = dotEnvDevEnv

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

// Coverage reports test coverage.
func Coverage() error {
	coverageFile := filepath.Join(os.
		TempDir(),
		fmt.Sprintf("coverage_%d.out", time.Now().UnixMilli()))

	if err := sh.Run("go", "test", "--coverprofile", coverageFile, "./..."); err != nil {
		return fmt.Errorf("failed to run tests with coverage: %w", err)
	}

	if err := sh.Run("go", "tool", "cover", "-html", coverageFile); err != nil {
		return fmt.Errorf("failed to run cover tool: %w", err)
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
	if err := stdmage.LoadEnv(devEnv); err != nil {
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
