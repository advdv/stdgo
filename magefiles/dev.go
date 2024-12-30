// Package main describes automation tasks.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/destel/rill"
	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

func init() {
	mustBeInRootIfNotTest()
}

// Dev namespace holds development commands.
type Dev mg.Namespace

// Lint our codebase.
func (Dev) Lint(ctx context.Context) error {
	if err := runForEachPackage(ctx, "go", "vet", "./..."); err != nil {
		return fmt.Errorf("failed to vet each package: %w", err)
	}

	return runForEachPackage(ctx, "golangci-lint", "run")
}

// Test the whole codebase.
func (Dev) Test(ctx context.Context) error {
	return runForEachPackage(ctx, "go", "test", "./...")
}

// Release tags a new version and pushes it.
func (Dev) Release(version string) error {
	if !regexp.MustCompile(`^v([0-9]+).([0-9]+).([0-9]+)$`).Match([]byte(version)) {
		return errors.New("version must be in format vX,Y,Z")
	}

	if err := sh.Run("git", "tag", version); err != nil {
		return fmt.Errorf("failed to tag version: %w", err)
	}

	if err := sh.Run("git", "push", "origin", version); err != nil {
		return fmt.Errorf("failed to push version tag: %w", err)
	}

	return nil
}

func runForEachPackage(ctx context.Context, cmd string, args ...string) error {
	return forEachPackageDir(func(e os.DirEntry) error {
		cmd := exec.CommandContext(ctx, cmd, args...)
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout
		cmd.Dir = e.Name()

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to run in sub package '%s': %w", e.Name(), err)
		}

		return nil
	})
}

func forEachPackageDir(fn func(e os.DirEntry) error) error {
	return rill.ForEach(rill.FromSlice(os.ReadDir(".")), runtime.NumCPU(), func(e os.DirEntry) error {
		if !e.IsDir() {
			return nil
		}

		if _, err := os.Stat(filepath.Join(e.Name(), "go.mod")); err != nil {
			return nil //nolint:nilerr
		}

		return fn(e)
	})
}

func mustBeInRootIfNotTest() {
	if _, err := os.ReadFile("go.work"); err != nil && !strings.Contains(strings.Join(os.Args, ""), "-test.") {
		panic("must be in project root, couldn't stat go.work file: " + err.Error())
	}
}
