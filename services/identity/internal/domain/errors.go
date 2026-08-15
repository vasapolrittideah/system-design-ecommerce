package domain

import "fmt"

// ValidationError reports a value this package refused.
//
// It declares its kind structurally — ErrorKind returns one of the strings
// pkg/errorx knows, and a method signature is not an import — which is what
// lets errorx.ToGRPC answer InvalidArgument for it while this package still
// depends on nothing but the standard library.
//
// The kinds are transport-shaped on purpose: "invalid_input", never
// "ErrEmailMalformed". What the failure means in this service's vocabulary is
// carried by Field and Message, and by the reason code an outer layer attaches.
type ValidationError struct {
	// Field is the name the caller used, so a client can point at the input it
	// got wrong. It is the proto field name where there is one.
	Field string

	// Message says what is wrong with it, and never repeats the value: an
	// email address is the user's, and this string reaches a log line.
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// ErrorKind implements the structural contract pkg/errorx resolves errors
// through. See pkg/errorx.Kinder for why it is a method rather than an import.
func (e ValidationError) ErrorKind() string { return "invalid_input" }
