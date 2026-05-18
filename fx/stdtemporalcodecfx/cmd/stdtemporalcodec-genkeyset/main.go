// Command stdtemporalcodec-genkeyset generates a fresh AES-256-GCM Tink
// keyset and prints it to stdout as a base64-encoded JSON keyset, suitable
// for use as the value of the STDTEMPORALCODEC_KEYSET and
// STDTEMPORALCODECSERVER_KEYSET environment variables consumed by
// stdtemporalcodecfx.
//
// Backends:
//
//   - Without --kek-uri the output is a cleartext keyset. Sensitive: it
//     contains the raw symmetric key material. Store it in your secrets
//     manager; never check it into source control.
//   - With --kek-uri aws-kms://<arn> the keyset is wrapped by the named
//     AWS KMS KEK before being emitted. The wrapped blob is safe to ship
//     via env/secret manager because it can only be unwrapped by callers
//     with kms:Decrypt on the KEK.
//
// Usage:
//
//	# cleartext keyset (local dev)
//	go run github.com/advdv/stdgo/fx/stdtemporalcodecfx/cmd/stdtemporalcodec-genkeyset
//
//	# KMS-wrapped keyset (production)
//	go run github.com/advdv/stdgo/fx/stdtemporalcodecfx/cmd/stdtemporalcodec-genkeyset \
//	    --kek-uri aws-kms://arn:aws:kms:us-east-1:111122223333:key/abcd-...
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/tink-crypto/tink-go-awskms/v3/integration/awskms"
	"github.com/tink-crypto/tink-go/v2/aead"
	"github.com/tink-crypto/tink-go/v2/insecurecleartextkeyset"
	"github.com/tink-crypto/tink-go/v2/keyset"
)

const awsKMSURIPrefix = "aws-kms://"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("stdtemporalcodec-genkeyset", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kekURI := fs.String("kek-uri", "",
		`optional KEK URI used to wrap the generated keyset (e.g. "aws-kms://arn:aws:kms:...")`)
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	handle, err := keyset.NewHandle(aead.AES256GCMKeyTemplate())
	if err != nil {
		return fmt.Errorf("new keyset handle: %w", err)
	}

	var buf bytes.Buffer
	switch {
	case *kekURI == "":
		if err := insecurecleartextkeyset.Write(handle, keyset.NewJSONWriter(&buf)); err != nil {
			return fmt.Errorf("write cleartext keyset: %w", err)
		}
	case strings.HasPrefix(*kekURI, awsKMSURIPrefix):
		kekAEAD, err := awsKEKAEAD(ctx, *kekURI)
		if err != nil {
			return err
		}
		if err := handle.Write(keyset.NewJSONWriter(&buf), kekAEAD); err != nil {
			return fmt.Errorf("write wrapped keyset: %w", err)
		}
		fmt.Fprintln(stderr, "wrapped keyset emitted; set STDTEMPORALCODEC_KEYSET_KEK_URI to:", *kekURI)
	default:
		return fmt.Errorf("unsupported KEK URI scheme: %q", *kekURI)
	}

	if _, err := fmt.Fprintln(stdout, base64.StdEncoding.EncodeToString(buf.Bytes())); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}

func awsKEKAEAD(ctx context.Context, kekURI string) (tinkAEAD, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load aws config for kms: %w", err)
	}
	client, err := awskms.NewClientWithOptions(ctx, kekURI, awskms.WithKMS(kms.NewFromConfig(cfg)))
	if err != nil {
		return nil, fmt.Errorf("build aws kms client: %w", err)
	}
	aead, err := client.GetAEAD(kekURI)
	if err != nil {
		return nil, fmt.Errorf("get aws kms aead: %w", err)
	}
	return aead, nil
}

// tinkAEAD mirrors the minimal interface we need from tink.AEAD without
// importing the full tink package in this CLI.
type tinkAEAD interface {
	Encrypt(plaintext, associatedData []byte) ([]byte, error)
	Decrypt(ciphertext, associatedData []byte) ([]byte, error)
}
