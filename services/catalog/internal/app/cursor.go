package app

import (
	"encoding/base64"
	"strings"
	"time"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/port/out"
)

// cursorSeparator divides the two halves of a page token. Neither an RFC 3339
// timestamp nor a UUID can contain it.
const cursorSeparator = "|"

// encodeCursor turns the end of a page into the token that asks for the next
// one.
//
// Encoded rather than returned as two readable fields so that it stays this
// service's business what a page boundary is made of: a client that learned to
// build one by hand would be a client that breaks when the ordering changes.
// It is obfuscation and not protection — anything a caller sends is checked as
// if they wrote it themselves.
func encodeCursor(cursor out.ProductCursor) string {
	raw := cursor.CreatedAt.UTC().Format(time.RFC3339Nano) + cursorSeparator + cursor.ID.String()

	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor reads a token back, and returns nil for the empty one — the
// first page asks for nothing in particular.
//
// Every failure is the same InvalidInput: a caller who cannot produce a valid
// token has no use for the difference between a bad encoding and a bad
// timestamp, and this is the one input on the listing that arrives already
// mangled by a URL more often than by a bug.
func decodeCursor(token string) (*out.ProductCursor, error) {
	if token == "" {
		return nil, nil
	}

	invalid := errorx.New(errorx.KindInvalidInput, "page_token is not a cursor this service issued")

	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, invalid
	}

	createdAt, id, found := strings.Cut(string(raw), cursorSeparator)
	if !found {
		return nil, invalid
	}

	at, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return nil, invalid
	}

	productID, err := domain.ParseProductID(id)
	if err != nil {
		return nil, invalid
	}

	return &out.ProductCursor{CreatedAt: at, ID: productID}, nil
}
