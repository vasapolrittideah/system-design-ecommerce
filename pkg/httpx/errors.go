package httpx

import (
	"errors"
	"net/http"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
)

// ErrorResponse is the body behind every non-2xx answer this API gives. One
// shape for every endpoint and every failure, so a client writes the handling
// once.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody is what the frontend actually reads.
type ErrorBody struct {
	// Code is the machine-stable reason a client branches on — OUT_OF_STOCK,
	// NOT_FOUND, INVALID_INPUT. It is deliberately finer-grained than the HTTP
	// status: 409 alone cannot say whether an order was already paid or a SKU
	// was sold out, and a client that has to tell those apart would otherwise
	// be left matching on prose.
	Code string `json:"code"`

	// Message is a human-readable explanation, in English. It is a developer
	// aid and a last-resort string, not UI copy — the frontend renders from
	// Code, and only Fields carries text meant to be shown to a person.
	Message string `json:"message"`

	// Fields is set when the request failed per-field validation, one entry
	// per failing field, each already translated into the caller's language.
	Fields []FieldError `json:"fields,omitempty"`

	// Metadata carries the facts behind Code — the SKU that was short, the
	// status an order was actually in — so a client can compose its own
	// message without parsing one.
	Metadata map[string]string `json:"metadata,omitempty"`

	// CorrelationID is the ID that ties this response to every log line and
	// span behind it, across every service the request touched. It is here so
	// a user can quote one string in a bug report and be findable.
	CorrelationID string `json:"correlationId,omitempty"`
}

// FieldError is one failing field of a request body.
type FieldError struct {
	// Field is the path as the client wrote it, in the same camelCase the
	// request used, with indexes for collections: items[0].quantity.
	Field string `json:"field"`

	// Code is the rule that failed — required, gte, email. A client that wants
	// its own wording for a rule keys on this.
	Code string `json:"code,omitempty"`

	// Message is the failure translated into the language the caller asked
	// for, ready to render next to the input.
	Message string `json:"message"`
}

// Statuser is implemented by an error that names its own HTTP status.
//
// It exists for the handful of failures HTTP distinguishes and gRPC does not —
// a body over the size limit is a 413, an unacceptable content type is a 415,
// and neither has a status code to be mapped from. Everything else resolves
// through [errorx.HTTPStatus], which is where the mapping belongs.
type Statuser interface {
	error
	HTTPStatus() int
}

// errEncodeResponse stands in when a handler's own response cannot be encoded.
// There is no reason code worth inventing for it: the client did nothing wrong
// and can do nothing about it.
var errEncodeResponse = errorx.New(errorx.KindInternal, "encode response body")

// WriteError answers err in the shape every client of this API expects.
//
// The status comes from [Statuser] when the error names one and from
// [errorx.HTTPStatus] otherwise, so an error that crossed the wire as a gRPC
// status and one raised locally land on the same code. The reason and metadata
// come from errorx either way, which is what lets a service attach
// OUT_OF_STOCK and a SKU on one side of the wire and have them arrive here.
//
// A 5xx body carries a fixed message. Everything below 500 is a fact the caller
// asked for and gets the real one.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	httpStatus := statusOf(err)

	body := ErrorBody{
		Code:          errorx.Reason(err),
		Message:       messageOf(err, httpStatus),
		Fields:        fieldsOf(err),
		Metadata:      errorx.Metadata(err),
		CorrelationID: logger.CorrelationID(r.Context()),
	}

	WriteJSON(w, r, httpStatus, ErrorResponse{Error: body})
}

func statusOf(err error) int {
	if statuser, ok := errors.AsType[Statuser](err); ok {
		return statuser.HTTPStatus()
	}

	return errorx.HTTPStatus(err)
}

// messageOf picks the prose for the body.
//
// A gRPC error's Error() reads "rpc error: code = NotFound desc = ...", which
// is a Go string about transport, not an answer — the status message is the
// part a caller was meant to see.
func messageOf(err error, httpStatus int) string {
	if httpStatus >= http.StatusInternalServerError {
		return "internal error"
	}

	if st, ok := status.FromError(err); ok {
		return st.Message()
	}

	return err.Error()
}

// fieldsOf collects per-field failures from either side of the boundary: a
// [ValidationError] raised here against a BFF request DTO, or a BadRequest
// detail on a status returned by a service, which is what protovalidate
// produces when a proto's own constraints reject a request.
//
// The second kind arrives already worded in English and cannot be translated —
// it was written against a proto field this API does not expose. It is passed
// through rather than dropped, because a field the frontend can name is worth
// more than a bare 400.
func fieldsOf(err error) []FieldError {
	if invalid, ok := errors.AsType[*ValidationError](err); ok {
		return invalid.Fields
	}

	st, ok := status.FromError(err)
	if !ok {
		return nil
	}

	for _, detail := range st.Details() {
		badRequest, ok := detail.(*errdetails.BadRequest)
		if !ok {
			continue
		}

		violations := badRequest.GetFieldViolations()
		fields := make([]FieldError, 0, len(violations))
		for _, violation := range violations {
			fields = append(fields, FieldError{
				Field:   violation.GetField(),
				Message: violation.GetDescription(),
			})
		}

		return fields
	}

	return nil
}
