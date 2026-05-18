package stdtemporalcodecfx

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/tink-crypto/tink-go-awskms/v3/integration/awskms"
	"github.com/tink-crypto/tink-go/v2/insecurecleartextkeyset"
	"github.com/tink-crypto/tink-go/v2/keyset"
	"github.com/tink-crypto/tink-go/v2/tink"
)

// AWSKMSURIPrefix is the URI scheme prefix that selects the AWS KMS
// backend for wrapping/unwrapping the Tink DEK keyset.
const AWSKMSURIPrefix = "aws-kms://"

// loadKeyset materializes a *keyset.Handle from a base64-encoded JSON
// Tink keyset. If kekURI is empty the input is treated as a cleartext
// keyset; if it starts with AWSKMSURIPrefix the input is treated as a
// KMS-wrapped keyset and unwrapped using a tink.AEAD backed by AWS KMS.
//
// kmsClient may be nil. It is only consulted when kekURI selects the AWS
// KMS backend; in that case a nil value triggers construction of a
// default *kms.Client from the ambient AWS SDK configuration.
func loadKeyset(ctx context.Context, encoded, kekURI string, kmsClient KMS) (*keyset.Handle, error) {
	if encoded == "" {
		return nil, errors.New("stdtemporalcodecfx: keyset is required")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("stdtemporalcodecfx: decode base64 keyset: %w", err)
	}

	var handle *keyset.Handle
	switch {
	case kekURI == "":
		handle, err = insecurecleartextkeyset.Read(keyset.NewJSONReader(bytes.NewReader(raw)))
		if err != nil {
			return nil, fmt.Errorf("stdtemporalcodecfx: read cleartext tink keyset: %w", err)
		}
	case strings.HasPrefix(kekURI, AWSKMSURIPrefix):
		kekAEAD, err := awsKEKAEAD(ctx, kekURI, kmsClient)
		if err != nil {
			return nil, err
		}
		handle, err = keyset.Read(keyset.NewJSONReader(bytes.NewReader(raw)), kekAEAD)
		if err != nil {
			return nil, fmt.Errorf("stdtemporalcodecfx: unwrap tink keyset with aws kms: %w", err)
		}
	default:
		return nil, fmt.Errorf("stdtemporalcodecfx: unsupported KEK URI scheme: %q", kekURI)
	}

	if handle.KeysetInfo().GetPrimaryKeyId() == 0 {
		return nil, errors.New("stdtemporalcodecfx: tink keyset has no primary key")
	}
	return handle, nil
}

// awsKEKAEAD returns a tink.AEAD that wraps/unwraps Tink keysets via the
// AWS KMS KEK addressed by kekURI. If kmsClient is nil a default
// *kms.Client is constructed from the ambient AWS SDK configuration.
func awsKEKAEAD(ctx context.Context, kekURI string, kmsClient KMS) (tink.AEAD, error) {
	if kmsClient == nil {
		cfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, fmt.Errorf("stdtemporalcodecfx: load aws config for kms: %w", err)
		}
		kmsClient = kms.NewFromConfig(cfg)
	}
	client, err := awskms.NewClientWithOptions(ctx, kekURI, awskms.WithKMS(kmsClient))
	if err != nil {
		return nil, fmt.Errorf("stdtemporalcodecfx: build aws kms client: %w", err)
	}
	aead, err := client.GetAEAD(kekURI)
	if err != nil {
		return nil, fmt.Errorf("stdtemporalcodecfx: get aws kms aead: %w", err)
	}
	return aead, nil
}
