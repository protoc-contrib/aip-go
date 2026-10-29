package aip

import (
	"errors"
	"fmt"
	"runtime"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
)

// NewErrorDetail packs message as an error detail, or returns nil, which
// [connect.Error.WithDetail] ignores, if it cannot be marshaled.
func NewErrorDetail(message proto.Message) *connect.ErrorDetail {
	detail, err := connectproto.NewErrorDetail(message)
	if err != nil {
		return nil
	}
	return detail
}

// NewErrorInfo returns the google.rpc.ErrorInfo detail of an error (AIP-193):
// why it happened, as reason, in domain, the name of the service that
// generated it. A reason is unique within its domain, at most 63 characters,
// and matches `[A-Z][A-Z0-9_]+[A-Z0-9]`, such as `ITEM_NOT_FOUND`.
func NewErrorInfo(domain, reason string) *connect.ErrorDetail {
	return NewErrorDetail(&errdetails.ErrorInfo{Reason: reason, Domain: domain})
}

// NewResourceInfo returns the google.rpc.ResourceInfo detail of an error about
// the resource named name: its type and its name.
func NewResourceInfo(name ResourceName) *connect.ErrorDetail {
	return NewErrorDetail(&errdetails.ResourceInfo{ResourceType: name.Type(), ResourceName: name.String()})
}

// NewRetryInfo returns the google.rpc.RetryInfo detail of an error the client
// may retry: how long to wait before it does (AIP-194).
func NewRetryInfo(delay time.Duration) *connect.ErrorDetail {
	return NewErrorDetail(&errdetails.RetryInfo{RetryDelay: durationpb.New(delay)})
}

// NewBadRequest returns the google.rpc.BadRequest detail of an error about the
// request field err names (AIP-193). err is a [*FieldError]: what a generated
// Parse method returns, or [NewFieldError] makes of any other failure. The
// description is fixed, since the error's own text can carry the value: the
// field says where, and the error's own detail says which kind. It panics if
// err names no field: a handler passing such an error is a bug, and one that
// should fail the first test that reaches it, not send a BadRequest naming
// nothing.
//
// A failure of the request's own rules is answered by
// [NewFieldBehaviorInterceptor], with a violation per field and its messages,
// so it needs no call here.
func NewBadRequest(err error) *connect.ErrorDetail {
	field, ok := errors.AsType[*FieldError](err)
	if !ok {
		panic(fmt.Sprintf("aip.NewBadRequest: %v names no field: it is not an *aip.FieldError", err))
	}
	return NewErrorDetail(&errdetails.BadRequest{
		FieldViolations: []*errdetails.BadRequest_FieldViolation{{Field: field.Field(), Description: "value is invalid"}},
	})
}

// NewDebugInfo returns the google.rpc.DebugInfo detail of an error caused by
// cause: cause's text, and the stack where the detail was built.
//
// AIP-193 keeps debugging information out of what a client sees:
// [NewErrorInterceptor] logs it and answers without it, for a failed RPC and
// for a failed entry of a batch response alike.
func NewDebugInfo(cause error) *connect.ErrorDetail {
	pcs := make([]uintptr, 32)
	// 2 skips runtime.Callers and NewDebugInfo: the stack starts at the caller
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs)])

	info := &errdetails.DebugInfo{Detail: cause.Error()}
	for {
		frame, more := frames.Next()
		info.StackEntries = append(info.StackEntries, fmt.Sprintf("%s %s:%d", frame.Function, frame.File, frame.Line))
		if !more {
			break
		}
	}
	return NewErrorDetail(info)
}

// NewStatus converts err into the [*status.Status] (google.rpc.Status) of a
// failed batch entry: the code, public message and details a unary error
// would carry, google.rpc.DebugInfo included: [NewErrorInterceptor] logs it and
// takes it out of the response. An error that is not a [*connect.Error] is
// Internal, with a fixed message, so no internal error text reaches the client.
// Returns nil when err is nil.
func NewStatus(err error) *status.Status {
	if err == nil {
		return nil
	}

	cerr, ok := errors.AsType[*connect.Error](err)
	if !ok {
		cerr = connect.NewError(connect.CodeInternal, "Unable to complete the operation at this time").WithCause(err)
	}
	result := &status.Status{
		Code:    int32(cerr.Code()),
		Message: cerr.Message(),
	}
	for _, detail := range cerr.Details() {
		result.Details = append(result.Details, &anypb.Any{
			TypeUrl: "type.googleapis.com/" + detail.Type,
			Value:   detail.Value,
		})
	}
	return result
}
