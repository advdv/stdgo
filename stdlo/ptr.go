package stdlo

// ToPtr returns a pointer copy of value.
//
//go:fix inline
func ToPtr[T any](x T) *T {
	return new(x)
}
