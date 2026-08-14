package httpx

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/go-playground/locales"
	enlocale "github.com/go-playground/locales/en"
	thlocale "github.com/go-playground/locales/th"
	ut "github.com/go-playground/universal-translator"
	"github.com/go-playground/validator/v10"
	entranslations "github.com/go-playground/validator/v10/translations/en"
	thtranslations "github.com/go-playground/validator/v10/translations/th"
	"golang.org/x/text/language"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
)

// DefaultLanguage is what a request gets when it asks for nothing, asks for
// something unparseable, or asks for a language this API does not speak.
const DefaultLanguage = "en"

// supported is every language the API can answer a validation failure in, with
// English first because it is both the fallback translator and the default the
// matcher falls back to.
//
// The set is fixed at compile time rather than configured, because adding a
// language means importing its locale and its translation catalogue — a code
// change either way. A config key that could only ever select a subset of what
// was already linked in would suggest a freedom that does not exist.
var supported = []struct {
	name       string
	tag        language.Tag
	translator func() locales.Translator
	register   func(*validator.Validate, ut.Translator) error
}{
	{"en", language.English, enlocale.New, entranslations.RegisterDefaultTranslations},
	{"th", language.Thai, thlocale.New, thtranslations.RegisterDefaultTranslations},
}

// Validator checks request bodies and explains the failures in the caller's
// language.
//
// One instance is built at startup and shared by every handler: it compiles
// struct tags on first use and caches them, so a per-request instance would
// throw that away and re-do the reflection on every call.
type Validator struct {
	validate *validator.Validate
	uni      *ut.UniversalTranslator
	matcher  language.Matcher
}

// MustNewValidator builds the validator and its translators, panicking if a
// translation catalogue fails to register.
//
// It panics rather than returning an error because the only way that happens is
// a mismatch between linked-in packages — a startup fault with no runtime
// remedy, in the same class as a bad regexp constant.
func MustNewValidator() *Validator {
	translators := make([]locales.Translator, 0, len(supported))
	tags := make([]language.Tag, 0, len(supported))

	for _, entry := range supported {
		translators = append(translators, entry.translator())
		tags = append(tags, entry.tag)
	}

	uni := ut.New(translators[0], translators...)

	validate := validator.New(validator.WithRequiredStructEnabled())
	validate.RegisterTagNameFunc(jsonFieldName)

	for _, entry := range supported {
		translator, found := uni.GetTranslator(entry.name)
		if !found {
			panic(fmt.Sprintf("httpx: no translator registered for %q", entry.name))
		}
		if err := entry.register(validate, translator); err != nil {
			panic(fmt.Sprintf("httpx: register %q translations: %v", entry.name, err))
		}
	}

	return &Validator{
		validate: validate,
		uni:      uni,
		matcher:  language.NewMatcher(tags),
	}
}

// ValidationError is a request body that decoded but broke its own rules.
//
// It declares itself invalid input through [errorx.Kinder] — the same
// structural contract a domain package uses — so [errorx.HTTPStatus] answers
// 400 for it without this package having to special-case anything.
type ValidationError struct {
	// Fields is one entry per failing field, already translated.
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	names := make([]string, 0, len(e.Fields))
	for _, field := range e.Fields {
		names = append(names, field.Field)
	}

	return "invalid request body: " + strings.Join(names, ", ")
}

// ErrorKind implements [errorx.Kinder].
func (e *ValidationError) ErrorKind() string { return string(errorx.KindInvalidInput) }

// Struct validates s and returns a [ValidationError] listing every field that
// failed, each message translated into the language negotiated for ctx by
// [Validator.Localize].
//
// Every failing field is reported, not just the first: a form that surfaces its
// errors one at a time turns a single correction into a round trip per field.
func (v *Validator) Struct(ctx context.Context, s any) error {
	err := v.validate.StructCtx(ctx, s)
	if err == nil {
		return nil
	}

	// Anything that is not a list of field failures means the handler passed
	// something unvalidatable — a bug here rather than a bad request. It must
	// not come back as a 400 telling the client to fix input that was never
	// read.
	failures, ok := errors.AsType[validator.ValidationErrors](err)
	if !ok {
		return errorx.Wrap(err, errorx.KindInternal, "validate request body")
	}

	translator := v.translator(ctx)

	fields := make([]FieldError, 0, len(failures))
	for _, failure := range failures {
		fields = append(fields, FieldError{
			Field:   fieldPath(failure),
			Code:    failure.Tag(),
			Message: failure.Translate(translator),
		})
	}

	return &ValidationError{Fields: fields}
}

// translator returns the translator for the language negotiated for ctx,
// falling back to English for a context that never passed through Localize.
func (v *Validator) translator(ctx context.Context) ut.Translator {
	translator, found := v.uni.GetTranslator(LanguageFrom(ctx))
	if !found {
		return v.uni.GetFallback()
	}

	return translator
}

// jsonFieldName makes the validator report a field by the name the client sent
// rather than the Go name it was decoded into, so a failure on unitPrice comes
// back as unitPrice and the frontend can attach it to the input the user typed
// in without a translation table of its own.
//
// An empty result tells the validator to keep the Go field name, which is what
// a field with no json tag, or one excluded from JSON entirely, should fall
// back to.
func jsonFieldName(field reflect.StructField) string {
	name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
	if name == "-" {
		return ""
	}

	return name
}

// fieldPath returns the failing field's full path with the request DTO's own
// name stripped: items[0].quantity, not AddCartItemRequest.items[0].quantity.
//
// The path matters as much as the name — a client rendering errors against a
// list of line items needs to know which line was rejected.
func fieldPath(failure validator.FieldError) string {
	namespace := failure.Namespace()
	if _, path, found := strings.Cut(namespace, "."); found {
		return path
	}

	return failure.Field()
}
