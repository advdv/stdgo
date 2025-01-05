// Package main provides automation targets for our domain model.
package main

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"

	//mage:import dev
	"github.com/advdv/stdgo/stdmage/stdmagedev"
)

func init() {
	stdmagedev.Init()
}

// Dev extends the dev namespace.
type Dev mg.Namespace

// Release reads a new version form the version.txt and pushes it as a tag.
func (Dev) Release() error {
	filename := "version.txt"
	version, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read version file: %w", err)
	}

	if !regexp.MustCompile(`^v([0-9]+).([0-9]+).([0-9]+)$`).Match(version) {
		return fmt.Errorf("%s: version must be in format vX,Y,Z", filename)
	}

	tagName := string(version)

	stderr := bytes.NewBuffer(nil)
	_, err = sh.Exec(nil, nil, stderr, "git", "tag", tagName)
	if err != nil && !strings.Contains(stderr.String(), "already exists") {
		return fmt.Errorf("failed to tag: %w", err)
	}

	if err := sh.Run("git", "push", "origin", tagName); err != nil {
		return fmt.Errorf("failed to push version tag: %w", err)
	}

	return nil
}
