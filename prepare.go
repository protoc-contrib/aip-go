package aip

import (
	"context"
	"errors"

	"connectrpc.com/connect/v2"
)

// RequestPreparer is a request that readies itself for its handler, filling
// in what AIP leaves to the server, such as a create's resource name
// (AIP-133). A service implements it on its request types, in a hand-written
// file next to the generated code.
type RequestPreparer interface {
	// Prepare fills in the request. An error rejects the request: as
	// InvalidArgument, with the field it names, when it is a [*FieldError].
	Prepare() error
}

// NewPrepareInterceptor returns a [connect.ServerInterceptor] that calls
// Prepare on each request message that implements [RequestPreparer], as the
// handler receives it. Install it after [NewFieldBehaviorInterceptor], so an
// omitted id is minted and every field validated by then.
//
// reject answers a request whose Prepare failed, with the error to return. It
// is where a service adds the details that are its own, such as a
// google.rpc.ErrorInfo; nil answers InvalidArgument with the fixed message
// "The request contains invalid parameters", the field Prepare names as a
// google.rpc.BadRequest when err is a [*FieldError], and the error as the
// cause, with its google.rpc.DebugInfo.
func NewPrepareInterceptor(reject func(err error) error) connect.ServerInterceptor {
	if reject == nil {
		reject = rejectPrepare
	}
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			return next(ctx, spec, &prepareStream{ServerStream: stream, reject: reject})
		}
	}
}

// prepareStream calls Prepare on each message the handler receives that
// implements [RequestPreparer].
type prepareStream struct {
	connect.ServerStream
	reject func(err error) error
}

func (s *prepareStream) Receive(message any) error {
	if err := s.ServerStream.Receive(message); err != nil {
		return err
	}
	if x, ok := message.(RequestPreparer); ok {
		if err := x.Prepare(); err != nil {
			return s.reject(err)
		}
	}
	return nil
}

// rejectPrepare is the default answer to a Prepare that failed.
func rejectPrepare(err error) error {
	rerr := connect.NewError(connect.CodeInvalidArgument, "The request contains invalid parameters")
	// A name Prepare cannot build is the caller's: the field it names.
	if _, ok := errors.AsType[*FieldError](err); ok {
		rerr = rerr.WithDetail(NewBadRequest(err))
	}
	return rerr.
		WithDetail(NewDebugInfo(err)).
		WithCause(err)
}
