package httpx

import (
	"net/http"
	"net/url"
	"reflect"
	"strconv"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
)

// queryTag names the query parameter a struct field is read from. A field
// without one is left alone, which is how a DTO carries fields the query string
// does not fill.
const queryTag = "query"

// BindQuery decodes r's query string into dst and validates it, so a listing
// endpoint checks its parameters through the same `validate` tags a request body
// goes through and a handler still checks nothing by hand.
//
// dst must be a non-nil pointer to a struct whose fields carry `query` tags —
// which are also the names failures are reported under, so a client that sent
// pageSize is told pageSize is wrong. Only strings, integers, and booleans are
// supported; anything else is a wiring mistake and comes back as Internal.
//
// Three decisions a caller can rely on:
//
//   - A parameter that is absent, or present but empty, leaves the field at its
//     zero value. `?category=` means the same as sending nothing, which is what
//     a form that submits its empty inputs actually does.
//   - A parameter repeated in the query string is read once, from its first
//     value.
//   - A parameter matching no field is ignored rather than rejected, unlike an
//     unknown field in a body. Links arrive carrying utm_source and a campaign
//     tag, and refusing those would break a URL that is otherwise correct.
func (v *Validator) BindQuery(r *http.Request, dst any) error {
	if err := decodeQuery(r.URL.Query(), dst); err != nil {
		return err
	}

	return v.Struct(r.Context(), dst)
}

func decodeQuery(values url.Values, dst any) error {
	pointer := reflect.ValueOf(dst)
	if pointer.Kind() != reflect.Pointer || pointer.IsNil() || pointer.Elem().Kind() != reflect.Struct {
		return errorx.New(errorx.KindInternal, "query target must be a non-nil pointer to a struct")
	}

	target := pointer.Elem()
	fields := target.Type()

	for i := range fields.NumField() {
		name := fields.Field(i).Tag.Get(queryTag)
		if name == "" || name == "-" {
			continue
		}

		raw := values.Get(name)
		if raw == "" {
			continue
		}

		if err := setQueryField(target.Field(i), name, raw); err != nil {
			return err
		}
	}

	return nil
}

func setQueryField(field reflect.Value, name, raw string) error {
	switch field.Kind() {
	case reflect.String:
		field.SetString(raw)
	case reflect.Bool:
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return errInvalidQueryValue(name, "boolean")
		}

		field.SetBool(parsed)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// Bits() rather than 64, so a value too large for the field it is
		// destined for is rejected here instead of silently wrapping.
		parsed, err := strconv.ParseInt(raw, 10, field.Type().Bits())
		if err != nil {
			return errInvalidQueryValue(name, "integer")
		}

		field.SetInt(parsed)
	default:
		return errorx.New(errorx.KindInternal, "query parameter %s has unsupported type %s", name, field.Type())
	}

	return nil
}

// errInvalidQueryValue reports a parameter that cannot become the type its field
// declares, under the same reason code the body decoder uses for the same
// mistake — a client that mistyped a number gets one answer to handle, whichever
// half of the request it was in.
func errInvalidQueryValue(name, expected string) error {
	return errorx.New(errorx.KindInvalidInput, "query parameter %s is not a valid %s", name, expected).
		WithReason("INVALID_FIELD_TYPE").
		WithMetadata(map[string]string{"field": name, "expected": expected})
}
