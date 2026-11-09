package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

// Pro checks the River Pro module (fx/stdriverprofx). It is a module of its
// own, which the dev targets do not reach, because downloading River Pro needs
// River Pro's license key. Its targets need that key in RIVER_PRO_SECRET; the
// module's mise.toml puts it in GOPROXY.
type Pro mg.Namespace

// proModuleDir is the directory of the River Pro module.
const proModuleDir = "fx/stdriverprofx"

var errBadVersion = errors.New("version must be in the format vX.Y.Z")

var errNoRiverProKey = errors.New(
	"RIVER_PRO_SECRET is not set: the River Pro module needs River Pro's license key to download riverpro")

// Test tests the River Pro module.
func (Pro) Test() error {
	return runPro("go", "test", "./...")
}

// Lint lints the River Pro module, with the root's golangci-lint config.
func (Pro) Lint() error {
	return runPro("golangci-lint", "run", "./...")
}

// Tidy tidies the River Pro module's go.mod and go.sum, such as after raising
// its version of stdgo or River Pro.
func (Pro) Tidy() error {
	return runPro("go", "mod", "tidy")
}

// runPro runs cmd in the River Pro module, in the environment of its
// mise.toml, outside any go.work, so it resolves stdgo at the version its
// go.mod requires, as its users do.
func runPro(cmd string, args ...string) error {
	if os.Getenv("RIVER_PRO_SECRET") == "" {
		return errNoRiverProKey
	}

	miseArgs := append([]string{"--cd", proModuleDir, "exec", "--", cmd}, args...)
	if err := sh.RunWithV(map[string]string{"GOWORK": "off"}, "mise", miseArgs...); err != nil {
		return fmt.Errorf("run %s in %s: %w", cmd, proModuleDir, err)
	}

	return nil
}

// Release tags the River Pro module's version, from its version.txt, and
// pushes the tag to origin. Go finds a module in a subdirectory by tags that
// start with its directory, such as fx/stdriverprofx/v0.0.1. It needs no key.
func (Pro) Release() error {
	filename := filepath.Join(proModuleDir, "version.txt")

	version, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("read version file: %w", err)
	}

	version = bytes.TrimSpace(version)
	if !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).Match(version) {
		return fmt.Errorf("%s: %w", filename, errBadVersion)
	}

	tagName := proModuleDir + "/" + string(version)

	stderr := bytes.NewBuffer(nil)
	if _, err := sh.Exec(nil, nil, stderr, "git", "tag", tagName); err != nil &&
		!strings.Contains(stderr.String(), "already exists") {
		return fmt.Errorf("tag %s: %w", tagName, err)
	}

	if err := sh.Run("git", "push", "origin", tagName); err != nil {
		return fmt.Errorf("push tag %s: %w", tagName, err)
	}

	return nil
}
