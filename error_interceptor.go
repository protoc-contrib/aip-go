package aip

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
)

// ErrorInterceptorOption configures [NewErrorInterceptor].
type ErrorInterceptorOption func(*errorInterceptor)

// WithErrorLogger sets where [NewErrorInterceptor] finds the logger of a
// request, such as the request-scoped one a logging middleware stored in its
// context. The default is [slog.Default].
func WithErrorLogger(logger func(context.Context) *slog.Logger) ErrorInterceptorOption {
	return func(i *errorInterceptor) { i.logger = logger }
}

// WithErrorRequestID sets how [NewErrorInterceptor] finds the id of a request,
// which it sends the client as a google.rpc.RequestInfo to quote. An empty id,
// and the default, which has none, send no RequestInfo.
func WithErrorRequestID(requestID func(context.Context) string) ErrorInterceptorOption {
	return func(i *errorInterceptor) { i.requestID = requestID }
}

type errorInterceptor struct {
	logger    func(context.Context) *slog.Logger
	requestID func(context.Context) string
}

func (i *errorInterceptor) log(ctx context.Context) *slog.Logger {
	if i.logger != nil {
		if logger := i.logger(ctx); logger != nil {
			return logger
		}
	}
	return slog.Default()
}

// requestInfo returns the google.rpc.RequestInfo detail of an error that
// answers the request ctx belongs to, or nil, which [connect.Error.WithDetail]
// ignores, if ctx has no request id.
func (i *errorInterceptor) requestInfo(ctx context.Context) *connect.ErrorDetail {
	if i.requestID == nil {
		return nil
	}
	id := i.requestID(ctx)
	if id == "" {
		return nil
	}
	return NewErrorDetail(&errdetails.RequestInfo{RequestId: id})
}

// NewErrorInterceptor returns a [connect.ServerInterceptor] that logs each
// failed RPC, with what failed underneath and where (its cause and its
// google.rpc.DebugInfo), which the client never sees. The log level is derived
// from the error's [connect.Code] by [ErrorLogLevel]: server-side failures at
// [slog.LevelError], everything else at [slog.LevelWarn]. The client gets the
// [*connect.Error] a handler answered with, less its google.rpc.DebugInfo,
// which only the log records, and with the id of the request as a
// google.rpc.RequestInfo, when [WithErrorRequestID] finds one; connect-go sends
// anything else as Unknown, with no message. A body that does not decode is
// answered with a fixed message, since the parser's text can carry the value:
// the interceptor wraps the stream, and an InvalidArgument the framework's own
// read returns is that failure. The log keeps the original as the cause.
//
// A batch response answers a failed entry with a [*status.Status], not an
// error, so the interceptor wraps the stream for the send too: it logs each
// entry that failed on the server's side, with its index and DebugInfo, and
// takes the DebugInfo out of every failed entry. A response is a batch's when
// it has a `responses` list whose items hold a google.rpc.Status.
//
// Install it first among the interceptors that read the request, so the stream
// it wraps is the one the framework reads, and treat the interceptor as the
// single logging boundary for RPC failures: handlers should return errors
// rather than log-and-return, otherwise failed requests log twice.
func NewErrorInterceptor(options ...ErrorInterceptorOption) connect.ServerInterceptor {
	interceptor := &errorInterceptor{}
	for _, option := range options {
		option(interceptor)
	}

	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			err := next(ctx, spec, errorStream{ServerStream: stream, ctx: ctx, interceptor: interceptor})
			if err == nil {
				return nil
			}
			message := err.Error()
			cerr, isConnect := errors.AsType[*connect.Error](err)
			if isConnect {
				message = cerr.Message()
			}
			attrs := []slog.Attr{slog.Any("error", err)}
			// What failed underneath, which the client never sees: a query
			// error, or a validation rule that failed to compile or evaluate,
			// which answers "validation failed".
			if cause := errors.Unwrap(err); cause != nil {
				attrs = append(attrs, slog.Any("cause", cause))
			}
			// Where it failed: the DebugInfo a handler attached.
			if isConnect {
				for _, detail := range cerr.Details() {
					if value, derr := connectproto.UnmarshalErrorDetail(detail); derr == nil {
						if debug, ok := value.(*errdetails.DebugInfo); ok {
							attrs = append(attrs, slog.Any("debug", debug))
						}
					}
				}
			}
			interceptor.log(ctx).LogAttrs(ctx, ErrorLogLevel(connect.CodeOf(err)), message, attrs...)

			// The DebugInfo is for the log, not the client (AIP-193); the request's
			// id is for the client, who may not see the response headers.
			if cerr, ok := errors.AsType[*connect.Error](err); ok {
				perr := connect.NewError(cerr.Code(), cerr.Message()).WithCause(errors.Unwrap(cerr))
				for _, detail := range cerr.Details() {
					if detail.Type != "google.rpc.DebugInfo" {
						perr = perr.WithDetail(detail)
					}
				}
				perr = perr.WithDetail(interceptor.requestInfo(ctx))
				return perr
			}
			return err
		}
	}
}

// errorStream is the request stream the framework reads, as
// [NewErrorInterceptor] hands it to the interceptors inside it. The interceptors
// after the error interceptor, the validation and the prepare hook, read through
// it and answer a request of their own only after the framework's read has
// succeeded, so an InvalidArgument that comes back from that read is a body that
// does not decode, and is the only one that can.
type errorStream struct {
	connect.ServerStream
	ctx         context.Context
	interceptor *errorInterceptor
}

// Receive reads the next request message, and answers one that does not decode
// with a fixed message and the framework's error as its cause.
func (s errorStream) Receive(msg any) error {
	err := s.ServerStream.Receive(msg)
	if cerr, ok := errors.AsType[*connect.Error](err); ok && cerr.Code() == connect.CodeInvalidArgument {
		return connect.NewError(connect.CodeInvalidArgument, "The request could not be processed due to malformed data").WithCause(err)
	}

	return err
}

// Send sends the next response message, after finding the failed entries of a
// batch response: the items of its `responses` list that hold a [*status.Status].
// Each one that is the server's, by its code, is logged with its index and its
// google.rpc.DebugInfo, and every one loses its DebugInfo, which is for the log,
// not the client (AIP-193). No handler answers a failed entry with an error, so
// this is the only place one is seen.
func (s errorStream) Send(msg any) error {
	response, ok := msg.(proto.Message)
	if !ok {
		return s.ServerStream.Send(msg)
	}

	message := response.ProtoReflect()
	field := message.Descriptor().Fields().ByName("responses")
	if field == nil || !field.IsList() || field.Message() == nil {
		return s.ServerStream.Send(msg)
	}

	items := message.Get(field).List()
	for index := range items.Len() {
		items.Get(index).Message().Range(func(fd protoreflect.FieldDescriptor, value protoreflect.Value) bool {
			if fd.Message() == nil || fd.Message().FullName() != "google.rpc.Status" {
				return true
			}
			entry, ok := value.Message().Interface().(*status.Status)
			if !ok {
				return true
			}

			var details []*anypb.Any
			var debug *errdetails.DebugInfo
			for _, detail := range entry.GetDetails() {
				info := &errdetails.DebugInfo{}
				if detail.MessageIs(info) && detail.UnmarshalTo(info) == nil {
					debug = info
					continue
				}
				details = append(details, detail)
			}
			entry.Details = details

			code := connect.Code(entry.GetCode())
			if ErrorLogLevel(code) == slog.LevelError {
				attrs := []slog.Attr{slog.Int("index", index), slog.String("code", code.String())}
				if debug != nil {
					attrs = append(attrs, slog.Any("debug", debug))
				}
				s.interceptor.log(s.ctx).LogAttrs(s.ctx, slog.LevelError, entry.GetMessage(), attrs...)
			}
			return false
		})
	}

	return s.ServerStream.Send(msg)
}

// ErrorLogLevel maps a [connect.Code] to the [slog.Level] used by
// [NewErrorInterceptor] when logging a failed RPC: server-side codes
// ([connect.CodeInternal], [connect.CodeUnknown], [connect.CodeDataLoss],
// [connect.CodeUnavailable]) at [slog.LevelError], everything else at
// [slog.LevelWarn] so client-induced failures stay out of the error stream.
func ErrorLogLevel(code connect.Code) slog.Level {
	switch code {
	case connect.CodeInternal, connect.CodeUnknown, connect.CodeDataLoss, connect.CodeUnavailable:
		return slog.LevelError
	default:
		return slog.LevelWarn
	}
}
