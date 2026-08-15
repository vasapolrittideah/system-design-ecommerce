package httpx

import (
	"context"
	"net/http"

	"golang.org/x/text/language"
)

type contextKey int

const languageKey contextKey = iota

// Localize resolves the response language from Accept-Language and puts it in
// the request context, where [Validator.Struct] picks it up.
//
// It is a method on Validator because the languages this API can answer in are
// exactly the catalogues the validator was built with; negotiating against any
// other list would settle on a language nothing can be written in.
//
// The result goes back as Content-Language, so a caller can see what it got.
// Anything unparseable, unsupported, or absent resolves to [DefaultLanguage].
//
// It must run before any handler that calls [Validator.Bind] or
// [Validator.Struct]; without it every message comes back in English.
func (v *Validator) Localize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := v.negotiate(r.Header.Get("Accept-Language"))
		w.Header().Set("Content-Language", name)

		next.ServeHTTP(w, r.WithContext(WithLanguage(r.Context(), name)))
	})
}

// negotiate picks the best supported language for an Accept-Language header.
//
// The header is a ranked list with quality weights, not a single value —
// "th-TH,th;q=0.9,en;q=0.8" is a caller who prefers Thai and will take English
// — so it is matched rather than compared. Matching also settles regional
// variants: th-TH is served by the th catalogue.
func (v *Validator) negotiate(header string) string {
	if header == "" {
		return DefaultLanguage
	}

	desired, _, err := language.ParseAcceptLanguage(header)
	if err != nil || len(desired) == 0 {
		return DefaultLanguage
	}

	_, index, confidence := v.matcher.Match(desired...)
	if confidence == language.No || index < 0 || index >= len(supported) {
		return DefaultLanguage
	}

	return supported[index].name
}

// WithLanguage stores the negotiated language in ctx. Localize calls it once
// per request; code that needs the language outside an HTTP handler — a worker
// rendering the same messages, say — can set it directly.
func WithLanguage(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, languageKey, name)
}

// LanguageFrom returns the language negotiated for ctx, or [DefaultLanguage]
// when there is none.
func LanguageFrom(ctx context.Context) string {
	name, ok := ctx.Value(languageKey).(string)
	if !ok || name == "" {
		return DefaultLanguage
	}

	return name
}
