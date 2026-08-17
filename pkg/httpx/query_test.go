package httpx_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/httpx"
)

// listProductsQuery is a listing endpoint's query DTO shaped the way this
// package expects them: query tags naming the parameters, rules in validate
// tags.
type listProductsQuery struct {
	Category  string `query:"category"  validate:"omitempty,max=64"`
	PageSize  int32  `query:"pageSize"  validate:"gte=0,lte=100"`
	InStock   bool   `query:"inStock"`
	PageToken string `query:"pageToken" validate:"omitempty,max=512"`
	Internal  string
}

func newQueryRequest(t *testing.T, rawQuery string) *http.Request {
	t.Helper()

	return httptest.NewRequest(http.MethodGet, "/api/v1/products?"+rawQuery, nil)
}

func TestBindQueryDecodesEverySupportedKind(t *testing.T) {
	v := httpx.MustNewValidator()

	var query listProductsQuery
	if err := v.BindQuery(newQueryRequest(t, "category=shirts&pageSize=50&inStock=true&pageToken=abc"),
		&query); err != nil {
		t.Fatalf("BindQuery returned an error: %v", err)
	}

	if query.Category != "shirts" || query.PageSize != 50 || !query.InStock || query.PageToken != "abc" {
		t.Errorf("decoded %+v, want shirts/50/true/abc", query)
	}
}

// A parameter that is absent and one that is present but empty mean the same
// thing, because a form that submits its empty inputs sends the second.
func TestBindQueryTreatsAnEmptyValueAsAbsent(t *testing.T) {
	v := httpx.MustNewValidator()

	query := listProductsQuery{Category: "untouched"}
	if err := v.BindQuery(newQueryRequest(t, "category=&pageSize="), &query); err != nil {
		t.Fatalf("BindQuery returned an error: %v", err)
	}

	if query.Category != "untouched" {
		t.Errorf("category = %q, want the field left alone", query.Category)
	}
}

// Unlike a body, where an unknown field is a client bug worth reporting, a query
// string carries whatever the link that produced it was built with.
func TestBindQueryIgnoresParametersItDoesNotName(t *testing.T) {
	v := httpx.MustNewValidator()

	var query listProductsQuery
	err := v.BindQuery(newQueryRequest(t, "utm_source=newsletter&Internal=set&category=shirts"), &query)
	if err != nil {
		t.Fatalf("BindQuery returned an error: %v", err)
	}

	if query.Category != "shirts" {
		t.Errorf("category = %q, want shirts", query.Category)
	}

	// A field with no query tag is not addressable from the URL, whatever the
	// caller names it.
	if query.Internal != "" {
		t.Errorf("Internal = %q, want a field without a query tag left alone", query.Internal)
	}
}

func TestBindQueryReportsAValueOfTheWrongType(t *testing.T) {
	v := httpx.MustNewValidator()

	var query listProductsQuery
	err := v.BindQuery(newQueryRequest(t, "pageSize=many"), &query)
	if err == nil {
		t.Fatal("BindQuery accepted a page size that is not a number")
	}

	if got := errorx.HTTPStatus(err); got != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", got, http.StatusBadRequest)
	}

	if got := errorx.Reason(err); got != "INVALID_FIELD_TYPE" {
		t.Errorf("reason = %q, want INVALID_FIELD_TYPE", got)
	}

	if got := errorx.Metadata(err)["field"]; got != "pageSize" {
		t.Errorf("metadata field = %q, want pageSize", got)
	}
}

// The field is int32, so a value that only fits in a wider integer is rejected
// here rather than wrapping into a small — or negative — page size.
func TestBindQueryRejectsAValueTooWideForItsField(t *testing.T) {
	v := httpx.MustNewValidator()

	var query listProductsQuery
	if err := v.BindQuery(newQueryRequest(t, "pageSize=4294967296"), &query); err == nil {
		t.Fatal("BindQuery accepted a page size that does not fit its field")
	}
}

// Validation runs on the decoded struct, and reports the parameter under the
// name the client wrote in the URL.
func TestBindQueryValidatesAndNamesTheParameter(t *testing.T) {
	v := httpx.MustNewValidator()

	var query listProductsQuery
	err := v.BindQuery(newQueryRequest(t, "pageSize=500"), &query)
	if err == nil {
		t.Fatal("BindQuery accepted a page size beyond the cap")
	}

	invalid, ok := errors.AsType[*httpx.ValidationError](err)
	if !ok {
		t.Fatalf("error = %v, want a *httpx.ValidationError", err)
	}

	if len(invalid.Fields) != 1 || invalid.Fields[0].Field != "pageSize" {
		t.Errorf("fields = %+v, want pageSize named once", invalid.Fields)
	}
}

// A target that cannot be decoded into is a wiring mistake, and must not come
// back as a 400 telling the client to fix input that was never read.
func TestBindQueryRejectsANonStructTarget(t *testing.T) {
	v := httpx.MustNewValidator()

	var target string
	err := v.BindQuery(newQueryRequest(t, "category=shirts"), &target)
	if err == nil {
		t.Fatal("BindQuery accepted a target it cannot decode into")
	}

	if got := errorx.HTTPStatus(err); got != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", got, http.StatusInternalServerError)
	}
}
