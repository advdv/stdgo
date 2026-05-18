package stdtemporalcodecfx

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/kms"
)

// KMS is the subset of the aws-sdk-go-v2 KMS client methods used by this
// package when unwrapping a Tink keyset that was sealed by an AWS KMS KEK.
// It matches awskms.KMSAPI in tink-go-awskms/v3 so any value implementing
// this interface (e.g. *kms.Client or a generated mock) can be plugged in
// via fx as an optional dependency. When no implementation is provided
// and a wrapping KEK URI requires one, a default *kms.Client constructed
// from the ambient AWS SDK configuration is used.
type KMS interface {
	Encrypt(ctx context.Context, params *kms.EncryptInput, optFns ...func(*kms.Options)) (*kms.EncryptOutput, error)
	Decrypt(ctx context.Context, params *kms.DecryptInput, optFns ...func(*kms.Options)) (*kms.DecryptOutput, error)
}
