package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
)

// MaxBodyBytes caps a request body at 1 MiB.
//
// This is the limit a client actually meets. Kong's cap at the edge is set
// deliberately higher, so that a body which is merely too large is refused here
// and answered as BODY_TOO_LARGE — an error Kong would have to answer in its
// own shape, having no way to produce this API's. Kong's remains the backstop
// for a body too big to be worth buffering at all.
const MaxBodyBytes int64 = 1 << 20

// unknownFieldPrefix is how encoding/json reports a field that is not in the
// target struct. It is a plain error string with no type behind it, so a prefix
// match is the only way to recognise it — checked against the current standard
// library wording and covered by a test that will fail if it ever changes.
const unknownFieldPrefix = `json: unknown field `

// Failures that mean the client sent something this API cannot read. They carry
// a reason code and no translated prose on purpose: each one is a bug in the
// caller, not something a person can fix by typing something different.
var (
	errUnsupportedMediaType = withStatus(http.StatusUnsupportedMediaType,
		errorx.New(errorx.KindInvalidInput, "content type must be %s", contentTypeJSON).
			WithReason("UNSUPPORTED_MEDIA_TYPE"))

	errBodyTooLarge = withStatus(http.StatusRequestEntityTooLarge,
		errorx.New(errorx.KindInvalidInput, "request body exceeds %d bytes", MaxBodyBytes).
			WithReason("BODY_TOO_LARGE"))

	errEmptyBody = errorx.New(errorx.KindInvalidInput, "request body is empty").
			WithReason("EMPTY_BODY")

	errTrailingContent = errorx.New(errorx.KindInvalidInput, "request body must contain a single JSON object").
				WithReason("MALFORMED_JSON")
)

// Bind decodes r's JSON body into dst and validates it, which is every check a
// handler should be doing to a request body and the only ones it should be
// doing itself.
//
// dst must be a non-nil pointer to a struct whose fields carry camelCase json
// tags and `validate` tags. Failures come back as one of the reason-coded
// decode errors, or as a [ValidationError] carrying every field that broke a
// rule, translated into the language [Validator.Localize] negotiated. Either
// way the handler's job is the same: hand it to [WriteError].
func (v *Validator) Bind(r *http.Request, dst any) error {
	if err := decode(r, dst); err != nil {
		return err
	}

	return v.Struct(r.Context(), dst)
}

// ReadBody returns the request body exactly as it arrived, capped at
// [MaxBodyBytes].
//
// For the one kind of request whose bytes are the point rather than their
// shape: a provider signs its webhook over precisely what it sent, so a body
// that has been decoded and re-encoded no longer verifies — a different key
// order is enough — and whatever forwards one has to pass the original through.
//
// It validates nothing beyond the size and the content type. What the body
// means belongs to whoever holds the signing secret, which is never this tier.
func ReadBody(r *http.Request) ([]byte, error) {
	if err := requireJSON(r); err != nil {
		return nil, err
	}

	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, MaxBodyBytes))
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return nil, errBodyTooLarge
		}

		return nil, errorx.Wrap(err, errorx.KindInvalidInput, "read request body").
			WithReason("MALFORMED_JSON")
	}

	if len(body) == 0 {
		return nil, errEmptyBody
	}

	return body, nil
}

func decode(r *http.Request, dst any) error {
	if err := requireJSON(r); err != nil {
		return err
	}

	// The nil writer means the server is not told to stop reading early; that
	// is a courtesy to a client already sending too much, not part of the
	// limit, and Bind has no ResponseWriter to hand over.
	body := http.MaxBytesReader(nil, r.Body, MaxBodyBytes)

	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		return decodeError(err)
	}

	// A body of "{} {}" decodes cleanly and silently discards the second half.
	// Rejecting it turns a client bug that would otherwise vanish into an
	// answer.
	if decoder.More() {
		return errTrailingContent
	}

	return nil
}

// requireJSON rejects a body this API cannot parse before reading any of it.
//
// A missing Content-Type is allowed: a request with no body at all is the
// common case, and an empty body is reported as such a moment later, which
// says more than a header complaint would.
func requireJSON(r *http.Request) error {
	header := r.Header.Get("Content-Type")
	if header == "" {
		return nil
	}

	mediaType, _, err := mime.ParseMediaType(header)
	if err != nil || mediaType != contentTypeJSON {
		return errUnsupportedMediaType
	}

	return nil
}

// decodeError turns encoding/json's failures into answers a client can act on.
//
// The standard library reports most of these as prose in a single error value,
// so the point of this function is to get back the one fact worth returning —
// which field, which offset — before it is flattened into a message.
func decodeError(err error) error {
	if errors.Is(err, io.EOF) {
		return errEmptyBody
	}

	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return errBodyTooLarge
	}

	if syntaxErr, ok := errors.AsType[*json.SyntaxError](err); ok {
		return errorx.New(errorx.KindInvalidInput, "malformed JSON at byte %d", syntaxErr.Offset).
			WithReason("MALFORMED_JSON").
			WithMetadata(map[string]string{"offset": strconv.FormatInt(syntaxErr.Offset, 10)})
	}

	// A body that ends mid-value is truncated rather than mistyped, and has no
	// offset worth reporting.
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return errorx.New(errorx.KindInvalidInput, "malformed JSON: unexpected end of body").
			WithReason("MALFORMED_JSON")
	}

	if typeErr, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		return errorx.New(errorx.KindInvalidInput, "field %s must be of type %s", typeErr.Field, typeErr.Type).
			WithReason("INVALID_FIELD_TYPE").
			WithMetadata(map[string]string{"field": typeErr.Field, "expected": typeErr.Type.String()})
	}

	if strings.HasPrefix(err.Error(), unknownFieldPrefix) {
		field := strings.Trim(strings.TrimPrefix(err.Error(), unknownFieldPrefix), `"`)

		return errorx.New(errorx.KindInvalidInput, "unknown field %s", field).
			WithReason("UNKNOWN_FIELD").
			WithMetadata(map[string]string{"field": field})
	}

	return errorx.Wrap(err, errorx.KindInvalidInput, "decode request body").
		WithReason("MALFORMED_JSON")
}

// statusError pins an HTTP status onto an error whose meaning gRPC has no code
// for, without giving up the reason and metadata errorx reads off the error it
// wraps.
type statusError struct {
	status int
	err    error
}

func withStatus(status int, err error) error {
	return &statusError{status: status, err: err}
}

func (e *statusError) Error() string { return e.err.Error() }

func (e *statusError) Unwrap() error { return e.err }

// HTTPStatus implements [Statuser].
func (e *statusError) HTTPStatus() int { return e.status }
