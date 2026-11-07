// Command stdopenapi-downconvert converts an OpenAPI 3.1 document to OpenAPI
// 3.0.3, for clients that do not support 3.1. It rewrites the 3.1 constructs
// that have a 3.0 equivalent, and fails on those that do not, as
// stdopenapi.DownConvert describes.
//
// Usage:
//
//	go tool stdopenapi-downconvert <in.yaml> <out.yaml>
//
// It reads YAML or JSON, and writes YAML.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/advdv/stdgo/stdopenapi"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "stdopenapi-downconvert:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: stdopenapi-downconvert <in.yaml> <out.yaml>")
	}

	in, err := os.ReadFile(args[0]) //nolint:gosec // reading the file named on the command line is the point.
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}

	out, err := stdopenapi.DownConvert(in)
	if err != nil {
		return fmt.Errorf("convert %s:\n%w", args[0], err)
	}

	//nolint:gosec // the converted document is as public as the one it reads.
	if err := os.WriteFile(args[1], out, 0o644); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	return nil
}
