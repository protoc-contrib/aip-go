package aip

import (
	"errors"
	"fmt"
	"runtime"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
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

// NameRequestError returns the [*FieldError] for err, a failure to read an id
// from the resource name a request carries, such as a wildcard where an id is
// needed. The field is the request's own: its `name`, or its `parent` where it
// has no `name` (AIP-131 to AIP-135). nil in, nil out, so a handler can test the
// result in place of err. It panics if req has neither field: a handler passing
// such a request is a bug.
func NameRequestError(req proto.Message, err error) error {
	if err == nil {
		return nil
	}
	fields := req.ProtoReflect().Descriptor().Fields()
	for _, name := range []string{"name", "parent"} {
		if fields.ByName(protoreflect.Name(name)) != nil {
			return NewFieldError(req, name, err)
		}
	}
	panic(fmt.Sprintf("aip.NameRequestError: %s has neither a name nor a parent field", req.ProtoReflect().Descriptor().FullName()))
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

// NewEntryStatus is [NewStatus] for the entry at index of a batch request's
// `requests`, with the fields of its google.rpc.BadRequest named under that
// entry, as `requests[1].name`: a field path is the request's, and the batch is
// the request a client sent.
func NewEntryStatus(err error, index int) *status.Status {
	result := NewStatus(err)
	if result == nil {
		return nil
	}
	for _, detail := range result.Details {
		request := &errdetails.BadRequest{}
		if !detail.MessageIs(request) || detail.UnmarshalTo(request) != nil {
			continue
		}
		for _, violation := range request.FieldViolations {
			violation.Field = fmt.Sprintf("requests[%d].%s", index, violation.Field)
		}
		_ = detail.MarshalFrom(request)
	}
	return result
}
