// Package main describes automation tasks.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
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

	ctx = context.WithValue(ctx, "concurrency", 1) //nolint:revive,staticcheck

	return runForEachPackage(ctx, "golangci-lint", "run")
}

// Test the whole codebase.
func (Dev) Test(ctx context.Context) error {
	mg.Deps((Dev{}).Serve)

	return runForEachPackage(ctx, "go", "test", "./...")
}

// Release tags a new version and pushes it.
func (Dev) Release(ctx context.Context) error {
	return forEachPackageDir(ctx, func(e PkgDirEntry) error {
		filename := filepath.Join(e.Path(), "version.txt")
		version, err := os.ReadFile(filename)
		if os.IsNotExist(err) {
			return nil // skip
		} else if err != nil {
			return fmt.Errorf("failed to read version file: %w", err)
		}

		if !regexp.MustCompile(`^v([0-9]+).([0-9]+).([0-9]+)$`).Match(version) {
			return errors.New("version must be in format vX,Y,Z")
		}

		tagName := fmt.Sprintf("%s/%s", e.TagName(), string(version))

		stderr := bytes.NewBuffer(nil)
		_, err = sh.Exec(nil, nil, stderr, "git", "tag", tagName)
		if err != nil && !strings.Contains(stderr.String(), "already exists") {
			return fmt.Errorf("failed to tag: %w", err)
		}

		if err := sh.Run("git", "push", "origin", tagName); err != nil {
			return fmt.Errorf("failed to push version tag: %w", err)
		}

		return nil
	})
}

// Serve serves out-of-process dependencies for testing.
func (Dev) Serve() error {
	if err := sh.RunWith(map[string]string{}, "docker", "compose",
		"-f", "docker-compose.yml",
		"up",
		"-d", "--build", "--remove-orphans", "--force-recreate",
	); err != nil {
		return fmt.Errorf("failed to run: %w", err)
	}

	return nil
}

func runForEachPackage(ctx context.Context, cmd string, args ...string) error {
	return forEachPackageDir(ctx, func(e PkgDirEntry) error {
		cmd := exec.CommandContext(ctx, cmd, args...)
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout
		cmd.Dir = e.Path()

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to run in sub package '%s': %w", e.Path(), err)
		}

		return nil
	})
}

type PkgDirEntry struct {
	SubDir string
	IsDir  bool
	Name   string
}

func (e PkgDirEntry) Path() string {
	if e.IsDir {
		return filepath.Join(e.SubDir, e.Name)
	}
	return e.Name
}

func (e PkgDirEntry) TagName() string {
	return path.Join(e.SubDir, e.Name)
}

func forEachPackageDir(ctx context.Context, fnc func(e PkgDirEntry) error) error {
	concurrency, ok := ctx.Value("concurrency").(int)
	if !ok {
		concurrency = runtime.NumCPU()
	}

	nonFxDirs := rill.Map(rill.FromSlice(os.ReadDir(".")), 1, func(e os.DirEntry) (PkgDirEntry, error) {
		return PkgDirEntry{"", e.IsDir(), e.Name()}, nil
	})

	fxDirs := rill.Map(rill.FromSlice(os.ReadDir("./fx/")), 1, func(e os.DirEntry) (PkgDirEntry, error) {
		return PkgDirEntry{"fx", e.IsDir(), e.Name()}, nil
	})

	return rill.ForEach(rill.Merge(nonFxDirs, fxDirs), concurrency, func(e PkgDirEntry) error {
		if !e.IsDir {
			return nil
		}

		filename := filepath.Join(e.Path(), "go.mod")
		if _, err := os.Stat(filename); err != nil {
			return nil //nolint:nilerr
		}

		return fnc(e)
	})
}

func mustBeInRootIfNotTest() {
	if _, err := os.ReadFile("go.work"); err != nil && !strings.Contains(strings.Join(os.Args, ""), "-test.") {
		panic("must be in project root, couldn't stat go.work file: " + err.Error())
	}
}
