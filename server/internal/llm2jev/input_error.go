package llm2jev

// InputError identifies a malformed argument without echoing its private value.
type InputError struct {
	Field    string
	Expected string
}

func (e *InputError) Error() string {
	return "llm2jev: invalid " + e.Field + ": " + e.Expected
}

// Unwrap preserves the provider-independent invalid-request contract.
func (e *InputError) Unwrap() error { return ErrInvalidRequest }

func invalidInput(field, expected string) error {
	return &InputError{Field: field, Expected: expected}
}
