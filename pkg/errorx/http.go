package errorx

import (
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// statusForCode is the second half of the journey: the Composition API holds
// gRPC errors from the services it fanned out to and has to answer in HTTP.
//
// It is keyed on the code rather than on [Kind] because by then the kind is
// gone — what crossed the wire was a status. Codes a service never produces
// are still listed, because a call can fail in the transport below the handler
// and the BFF has to answer something sane for those too.
var statusForCode = map[codes.Code]int{
	codes.OK:                 http.StatusOK,
	codes.NotFound:           http.StatusNotFound,
	codes.InvalidArgument:    http.StatusBadRequest,
	codes.OutOfRange:         http.StatusBadRequest,
	codes.FailedPrecondition: http.StatusConflict,
	codes.AlreadyExists:      http.StatusConflict,
	codes.Aborted:            http.StatusConflict,
	codes.PermissionDenied:   http.StatusForbidden,
	codes.Unauthenticated:    http.StatusUnauthorized,
	codes.ResourceExhausted:  http.StatusTooManyRequests,
	codes.Unimplemented:      http.StatusNotImplemented,
	codes.Unavailable:        http.StatusServiceUnavailable,
	codes.DeadlineExceeded:   http.StatusGatewayTimeout,

	// 499 is nginx's "client closed request", which Kong and every log
	// pipeline in front of this already understand. There is no standard code
	// for it, and answering 500 would count a caller who hung up as an outage.
	codes.Canceled: 499,
}

// HTTPStatus returns the HTTP status code err should be answered with.
//
// It accepts both sides of the boundary: an error still carrying a [Kind],
// which it maps through the same table [ToGRPC] uses, and a gRPC status
// returned by a downstream service, which it maps from the code. Anything it
// cannot place is a 500, for the same reason an unclassified error is Internal.
func HTTPStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}

	code := status.Code(err)
	if kind, ok := declaredKind(err); ok {
		code = codeForKind[kind]
	}

	if httpStatus, ok := statusForCode[code]; ok {
		return httpStatus
	}

	return http.StatusInternalServerError
}
