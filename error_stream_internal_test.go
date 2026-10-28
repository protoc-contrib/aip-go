package aip

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// fakeStream is a connect.ServerStream that hands out one received message and
// keeps what is sent.
type fakeStream struct {
	received func(msg any) error
	sent     []any
}

func (s *fakeStream) Receive(msg any) error {
	if s.received == nil {
		return nil
	}
	return s.received(msg)
}

func (s *fakeStream) SendHeaders() error { return nil }

func (s *fakeStream) Send(msg any) error {
	s.sent = append(s.sent, msg)
	return nil
}

// recordedLogs keeps the records it is asked to log.
type recordedLogs struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordedLogs) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordedLogs) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, record)
	return nil
}

func (h *recordedLogs) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordedLogs) WithGroup(string) slog.Handler      { return h }

// batchResponse returns a message shaped like a batch response: a `responses`
// list of items, each with a google.rpc.Status `error`, as the AIP-231..235
// batches of a service built on this library are.
func batchResponse(statuses ...*status.Status) proto.Message {
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:       proto.String("batch_test.proto"),
		Package:    proto.String("batchtest"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"google/rpc/status.proto"},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("Item"),
				Field: []*descriptorpb.FieldDescriptorProto{{
					Name:     proto.String("error"),
					Number:   proto.Int32(2),
					Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
					Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
					TypeName: proto.String(".google.rpc.Status"),
				}},
			},
			{
				Name: proto.String("Response"),
				Field: []*descriptorpb.FieldDescriptorProto{{
					Name:     proto.String("responses"),
					Number:   proto.Int32(1),
					Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
					Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
					TypeName: proto.String(".batchtest.Item"),
				}},
			},
		},
	}, protoregistry.GlobalFiles)
	Expect(err).NotTo(HaveOccurred())

	item := file.Messages().ByName("Item")
	response := dynamicpb.NewMessage(file.Messages().ByName("Response"))
	list := response.Mutable(response.Descriptor().Fields().ByName("responses")).List()
	for _, entry := range statuses {
		message := dynamicpb.NewMessage(item)
		if entry != nil {
			message.Set(item.Fields().ByName("error"), protoreflect.ValueOfMessage(entry.ProtoReflect()))
		}
		list.Append(protoreflect.ValueOfMessage(message))
	}
	return response
}

// failedEntry returns the status of a failed entry: a code, a message, and the
// details of the error a handler answered with.
func failedEntry(code connect.Code, cause error) *status.Status {
	return NewStatus(connect.NewError(code, "entry failed").WithDetail(NewDebugInfo(cause)))
}

var _ = Describe("errorStream", func() {
	var (
		logs        *recordedLogs
		interceptor *errorInterceptor
		inner       *fakeStream
		stream      errorStream
	)

	BeforeEach(func() {
		logs = &recordedLogs{}
		interceptor = &errorInterceptor{logger: func(context.Context) *slog.Logger { return slog.New(logs) }}
		inner = &fakeStream{}
		stream = errorStream{ServerStream: inner, ctx: context.Background(), interceptor: interceptor}
	})

	Describe("Receive", func() {
		It("answers a body that does not decode with a fixed message and the original as its cause", func() {
			original := connect.NewError(connect.CodeInvalidArgument, `proto: cannot parse "secret"`)
			inner.received = func(any) error { return original }

			err := stream.Receive(&errdetails.RetryInfo{})

			var cerr *connect.Error
			Expect(errors.As(err, &cerr)).To(BeTrue())
			Expect(cerr.Code()).To(Equal(connect.CodeInvalidArgument))
			Expect(cerr.Message()).To(Equal("The request could not be processed due to malformed data"))
			Expect(errors.Is(err, original)).To(BeTrue(), "the log keeps the original")
		})

		It("passes any other error through", func() {
			original := connect.NewError(connect.CodeUnavailable, "down")
			inner.received = func(any) error { return original }

			Expect(stream.Receive(&errdetails.RetryInfo{})).To(BeIdenticalTo(original))
		})
	})

	Describe("Send", func() {
		It("takes the DebugInfo out of each failed entry and logs the ones that are the server's", func() {
			response := batchResponse(
				failedEntry(connect.CodeInternal, errors.New("the insert failed")),
				failedEntry(connect.CodeNotFound, errors.New("no rows")),
				nil, // an entry that succeeded holds no status
			)

			Expect(stream.Send(response)).To(Succeed())

			Expect(inner.sent).To(HaveLen(1))
			Expect(logs.records).To(HaveLen(1), "only the Internal entry is the server's")
			Expect(logs.records[0].Level).To(Equal(slog.LevelError))
			Expect(logs.records[0].Message).To(Equal("entry failed"))
			attrs := map[string]slog.Value{}
			logs.records[0].Attrs(func(attr slog.Attr) bool {
				attrs[attr.Key] = attr.Value
				return true
			})
			Expect(attrs["index"].Int64()).To(Equal(int64(0)))
			Expect(attrs).To(HaveKey("debug"))
		})

		It("leaves no DebugInfo in any failed entry it sends", func() {
			internal := failedEntry(connect.CodeInternal, errors.New("the insert failed"))
			notFound := failedEntry(connect.CodeNotFound, errors.New("no rows"))

			Expect(stream.Send(batchResponse(internal, notFound))).To(Succeed())

			Expect(internal.GetDetails()).To(BeEmpty())
			Expect(notFound.GetDetails()).To(BeEmpty())
		})

		Describe("with a default ErrorInfo", func() {
			// reasons returns the ErrorInfo reason and domain of each entry's details.
			infoOf := func(entry *status.Status) []string {
				var found []string
				for _, detail := range entry.GetDetails() {
					info := &errdetails.ErrorInfo{}
					if detail.MessageIs(info) && detail.UnmarshalTo(info) == nil {
						found = append(found, info.GetReason()+"@"+info.GetDomain())
					}
				}
				return found
			}

			BeforeEach(func() {
				interceptor.domain = "books.example.com"
			})

			It("gives a failed entry that has none an ErrorInfo for its code", func() {
				entry := failedEntry(connect.CodeNotFound, errors.New("no rows"))

				Expect(stream.Send(batchResponse(entry))).To(Succeed())

				Expect(infoOf(entry)).To(ConsistOf("NOT_FOUND@books.example.com"))
			})

			It("leaves the ErrorInfo a failed entry has as it is", func() {
				entry := NewStatus(connect.NewError(connect.CodeNotFound, "entry failed").
					WithDetail(NewErrorInfo("shelves.example.com", "BOOK_NOT_FOUND")).
					WithDetail(NewDebugInfo(errors.New("no rows"))))

				Expect(stream.Send(batchResponse(entry))).To(Succeed())

				Expect(infoOf(entry)).To(ConsistOf("BOOK_NOT_FOUND@shelves.example.com"))
			})
		})

		It("adds no ErrorInfo to a failed entry without the option", func() {
			entry := failedEntry(connect.CodeNotFound, errors.New("no rows"))

			Expect(stream.Send(batchResponse(entry))).To(Succeed())

			Expect(entry.GetDetails()).To(BeEmpty())
		})

		It("sends a message that is not a batch response as it is", func() {
			message := &errdetails.RetryInfo{}

			Expect(stream.Send(message)).To(Succeed())

			Expect(inner.sent).To(ConsistOf(message))
			Expect(logs.records).To(BeEmpty())
		})
	})
})

// preparable is a request whose Prepare fails with err, or fills it in.
type preparable struct {
	err      error
	prepared bool
}

func (p *preparable) Prepare() error {
	p.prepared = true
	return p.err
}

var _ = Describe("prepareStream", func() {
	run := func(request any, reject func(error) error) error {
		next := func(_ context.Context, _ connect.Spec, stream connect.ServerStream) error {
			return stream.Receive(request)
		}
		return NewPrepareInterceptor(reject)(next)(context.Background(), connect.Spec{}, &fakeStream{})
	}

	It("prepares a request that is a RequestPreparer", func() {
		request := &preparable{}

		Expect(run(request, nil)).To(Succeed())

		Expect(request.prepared).To(BeTrue())
	})

	It("passes a message that is not a RequestPreparer through", func() {
		Expect(run(&errdetails.RetryInfo{}, nil)).To(Succeed())
	})

	It("rejects a failed Prepare as InvalidArgument, with the field it names", func() {
		field := NewFieldError(&errdetails.RetryInfo{}, "retry_delay", errors.New("is empty"))

		err := run(&preparable{err: field}, nil)

		var cerr *connect.Error
		Expect(errors.As(err, &cerr)).To(BeTrue())
		Expect(cerr.Code()).To(Equal(connect.CodeInvalidArgument))
		Expect(cerr.Message()).To(Equal("The request contains invalid parameters"))
		Expect(errors.Is(err, field)).To(BeTrue())
		var types []string
		for _, detail := range cerr.Details() {
			types = append(types, detail.Type)
		}
		Expect(types).To(ConsistOf("google.rpc.BadRequest", "google.rpc.DebugInfo"))
	})

	It("names no field for a failure that is not a *FieldError", func() {
		err := run(&preparable{err: errors.New("the id could not be minted")}, nil)

		var cerr *connect.Error
		Expect(errors.As(err, &cerr)).To(BeTrue())
		var types []string
		for _, detail := range cerr.Details() {
			types = append(types, detail.Type)
		}
		Expect(types).To(ConsistOf("google.rpc.DebugInfo"))
	})

	It("answers with the error a service's own reject returns", func() {
		answer := connect.NewError(connect.CodeFailedPrecondition, "mine")

		err := run(&preparable{err: errors.New("bad")}, func(error) error { return answer })

		Expect(err).To(BeIdenticalTo(answer))
	})
})
