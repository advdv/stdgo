package stdlo

// ToPtr returns a pointer copy of value.
func ToPtr[T any](x T) *T {
	return &x
}
