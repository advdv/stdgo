// Command stdtemporalcodec-genkeyset generates a fresh AES-256-GCM Tink
// keyset and prints it to stdout as a base64-encoded JSON cleartext keyset,
// suitable for use as the value of the STDTEMPORALCODEC_KEYSET and
// STDTEMPORALCODECSERVER_KEYSET environment variables consumed by
// stdtemporalcodecfx.
//
// The output is sensitive: it contains the raw symmetric key material.
// Store it in your secrets manager; never check it into source control.
//
// Usage:
//
//	go run github.com/advdv/stdgo/fx/stdtemporalcodecfx/cmd/stdtemporalcodec-genkeyset
package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"

	"github.com/tink-crypto/tink-go/v2/aead"
	"github.com/tink-crypto/tink-go/v2/insecurecleartextkeyset"
	"github.com/tink-crypto/tink-go/v2/keyset"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	handle, err := keyset.NewHandle(aead.AES256GCMKeyTemplate())
	if err != nil {
		return fmt.Errorf("new keyset handle: %w", err)
	}
	var buf bytes.Buffer
	if err := insecurecleartextkeyset.Write(handle, keyset.NewJSONWriter(&buf)); err != nil {
		return fmt.Errorf("write keyset: %w", err)
	}
	if _, err := fmt.Fprintln(os.Stdout, base64.StdEncoding.EncodeToString(buf.Bytes())); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}
