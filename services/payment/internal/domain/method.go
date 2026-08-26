package domain

import "strings"

// methodMax bounds a method name, matching the length the proto declares.
const methodMax = 32

// Method is how the customer chose to pay, in this system's vocabulary rather
// than a provider's.
//
// Deliberately not an enum. Which methods are on offer is a question about the
// shop and the provider it has signed with — PromptPay exists in Thailand and
// nowhere else, and a provider adds one without asking — so a closed set here
// would make every new method a deploy of this service, and an old row
// unreadable the day one is retired. What this package holds is the shape, and
// which names mean anything is the provider adapter's to answer.
//
// The empty method is legitimate and means "whatever the provider defaults to",
// which is what a hosted checkout page decides for itself.
type Method string

// NewMethod normalises and checks a method name.
func NewMethod(s string) (Method, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", nil
	}

	invalid := ValidationError{Field: "method", Message: "is not a lowercase method name"}

	if len(s) > methodMax {
		return "", invalid
	}

	// ^[a-z][a-z0-9_]*$, the same shape the proto declares. The first
	// character is a letter so that a name is never mistaken for a number by
	// whatever ends up reading these.
	for i := range len(s) {
		c := s[i]

		valid := c >= 'a' && c <= 'z' ||
			i > 0 && (c >= '0' && c <= '9' || c == '_')

		if !valid {
			return "", invalid
		}
	}

	return Method(s), nil
}

// String returns the normalised name, empty for the provider's default.
func (m Method) String() string { return string(m) }
