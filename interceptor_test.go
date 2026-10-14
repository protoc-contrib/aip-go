package aip_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"uuid"

	validatepb "buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/protoc-contrib/aip-go"
	testpb "github.com/protoc-contrib/aip-go/internal/testpb"
	"github.com/protoc-contrib/aip-go/internal/testpb/testpbconnect"
)

// bookService records what its handlers receive, and returns books that carry
// a server-owned etag and a client-owned request token, so a spec can see
// what the interceptor cleared on the way in and out.
type bookService struct {
	mu       sync.Mutex
	received []proto.Message
}

func (s *bookService) record(message proto.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.received = append(s.received, proto.Clone(message))
}

func (s *bookService) last() proto.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.received[len(s.received)-1]
}

func returned(book *testpb.Book) *testpb.Book {
	book = proto.Clone(book).(*testpb.Book)
	book.Etag = "server-etag"
	book.RequestToken = "leaked"
	return book
}

func (s *bookService) CreateBook(_ context.Context, req *connect.Request[testpb.CreateBookRequest]) (*connect.Response[testpb.Book], error) {
	s.record(req.Msg)
	return connect.NewResponse(returned(req.Msg.GetBook())), nil
}

func (s *bookService) UpdateBook(_ context.Context, req *connect.Request[testpb.UpdateBookRequest]) (*connect.Response[testpb.Book], error) {
	s.record(req.Msg)
	return connect.NewResponse(returned(req.Msg.GetBook())), nil
}

func (s *bookService) BatchCreateBooks(_ context.Context, req *connect.Request[testpb.BatchCreateBooksRequest]) (*connect.Response[testpb.BatchCreateBooksResponse], error) {
	s.record(req.Msg)
	response := &testpb.BatchCreateBooksResponse{}
	for _, entry := range req.Msg.GetRequests() {
		response.Books = append(response.Books, returned(entry.GetBook()))
	}
	return connect.NewResponse(response), nil
}

func (s *bookService) GetBook(_ context.Context, req *connect.Request[testpb.GetBookRequest]) (*connect.Response[testpb.Book], error) {
	s.record(req.Msg)
	return connect.NewResponse(returned(&testpb.Book{Name: req.Msg.GetName()})), nil
}

func (s *bookService) SyncBooks(_ context.Context, stream *connect.BidiStream[testpb.Book, testpb.Book]) error {
	for {
		book, err := stream.Receive()
		if err != nil {
			return nil //nolint:nilerr // the client closed its side
		}
		s.record(book)
		if err := stream.Send(returned(book)); err != nil {
			return err
		}
	}
}

// violations returns the field path and rule id of each violation in the
// buf.validate.Violations detail err carries.
func violations(err error) []string {
	var cerr *connect.Error
	Expect(errors.As(err, &cerr)).To(BeTrue(), "%v is not a *connect.Error", err)
	Expect(cerr.Code()).To(Equal(connect.CodeInvalidArgument))
	var found []string
	for _, detail := range cerr.Details() {
		value, err := detail.Value()
		Expect(err).NotTo(HaveOccurred())
		if v, ok := value.(*validatepb.Violations); ok {
			for _, violation := range v.GetViolations() {
				found = append(found, protovalidate.FieldPathString(violation.GetField())+" "+violation.GetRuleId())
			}
		}
	}
	return found
}

var _ = Describe("FieldBehaviorInterceptor", func() {
	var (
		service *bookService
		server  *httptest.Server
		client  testpbconnect.BookServiceClient
	)

	BeforeEach(func() {
		interceptor, err := aip.NewFieldBehaviorInterceptor()
		Expect(err).NotTo(HaveOccurred())

		service = &bookService{}
		mux := http.NewServeMux()
		mux.Handle(testpbconnect.NewBookServiceHandler(service, connect.WithInterceptors(interceptor)))
		// HTTP/2 over TLS, so the bidirectional stream works.
		server = httptest.NewUnstartedServer(mux)
		server.EnableHTTP2 = true
		server.StartTLS()
		DeferCleanup(server.Close)
		client = testpbconnect.NewBookServiceClient(server.Client(), server.URL)
	})

	Describe("a unary create", func() {
		It("mints a UUIDv7 id when the client omits one, leaving the name and parent to the handler", func(ctx SpecContext) {
			_, err := client.CreateBook(ctx, connect.NewRequest(&testpb.CreateBookRequest{
				Parent: "shelves/a",
				Book:   &testpb.Book{Name: "books/sent", Title: "Dune", Author: "Herbert"},
			}))
			Expect(err).NotTo(HaveOccurred())

			request := service.last().(*testpb.CreateBookRequest)
			id, err := uuid.Parse(request.GetBookId())
			Expect(err).NotTo(HaveOccurred())
			// The version is the high nibble of byte 6 (RFC 9562).
			Expect(id[6] >> 4).To(Equal(byte(7)))
			Expect(request.GetParent()).To(Equal("shelves/a"))
			Expect(request.GetBook().GetName()).To(Equal("books/sent"))
		})

		It("keeps an id the client sent", func(ctx SpecContext) {
			_, err := client.CreateBook(ctx, connect.NewRequest(&testpb.CreateBookRequest{
				Parent: "shelves/a",
				BookId: "dune",
				Book:   &testpb.Book{Title: "Dune", Author: "Herbert"},
			}))
			Expect(err).NotTo(HaveOccurred())
			request := service.last().(*testpb.CreateBookRequest)
			Expect(request.GetBookId()).To(Equal("dune"))
			Expect(request.GetBook().GetName()).To(BeEmpty())
		})

		It("clears OUTPUT_ONLY fields on the way in and INPUT_ONLY fields on the way out", func(ctx SpecContext) {
			response, err := client.CreateBook(ctx, connect.NewRequest(&testpb.CreateBookRequest{
				Parent: "shelves/a",
				Book:   &testpb.Book{Title: "Dune", Author: "Herbert", Etag: "client-etag", RequestToken: "token"},
			}))
			Expect(err).NotTo(HaveOccurred())

			received := service.last().(*testpb.CreateBookRequest).GetBook()
			Expect(received.GetEtag()).To(BeEmpty())
			Expect(received.GetRequestToken()).To(Equal("token"))
			Expect(response.Msg.GetEtag()).To(Equal("server-etag"))
			Expect(response.Msg.GetRequestToken()).To(BeEmpty())
		})

		It("reports protovalidate's and REQUIRED violations in one error", func(ctx SpecContext) {
			_, err := client.CreateBook(ctx, connect.NewRequest(&testpb.CreateBookRequest{
				Parent: "not-a-shelf",
				Book:   &testpb.Book{},
			}))
			Expect(violations(err)).To(Equal([]string{
				"parent string.pattern",
				"book.title required",
				"book.author required",
			}))
			Expect(service.received).To(BeEmpty())
		})

		It("reads like protovalidate's own `required` violation", func(ctx SpecContext) {
			_, err := client.CreateBook(ctx, connect.NewRequest(&testpb.CreateBookRequest{
				Parent: "shelves/a",
				Book:   &testpb.Book{Author: "Herbert"},
			}))
			Expect(err).To(MatchError(ContainSubstring("validation error: book.title: value is required")))
		})
	})

	Describe("a unary update", func() {
		It("implies an omitted mask from the fields the update sets", func(ctx SpecContext) {
			_, err := client.UpdateBook(ctx, connect.NewRequest(&testpb.UpdateBookRequest{
				Book: &testpb.Book{Name: "books/dune", Notes: "a classic", Etag: "client-etag"},
			}))
			Expect(err).NotTo(HaveOccurred())
			Expect(service.last().(*testpb.UpdateBookRequest).GetUpdateMask().GetPaths()).To(Equal([]string{"notes"}))
		})

		It("lets a partial update omit a REQUIRED field its mask does not name", func(ctx SpecContext) {
			_, err := client.UpdateBook(ctx, connect.NewRequest(&testpb.UpdateBookRequest{
				Book:       &testpb.Book{Name: "books/dune", Notes: "a classic"},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
			}))
			Expect(err).NotTo(HaveOccurred())
		})

		It("ignores the OUTPUT_ONLY fields an explicit mask names, rather than refusing them", func(ctx SpecContext) {
			_, err := client.UpdateBook(ctx, connect.NewRequest(&testpb.UpdateBookRequest{
				Book:       &testpb.Book{Name: "books/dune", Notes: "a classic", Etag: "client-etag"},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"etag", "notes", "courier.tracking_id"}},
			}))
			Expect(err).NotTo(HaveOccurred())
			Expect(service.last().(*testpb.UpdateBookRequest).GetUpdateMask().GetPaths()).To(Equal([]string{"notes"}))
		})

		It("leaves a mask that names only OUTPUT_ONLY fields empty, rather than implying one", func(ctx SpecContext) {
			_, err := client.UpdateBook(ctx, connect.NewRequest(&testpb.UpdateBookRequest{
				Book:       &testpb.Book{Name: "books/dune", Notes: "a classic"},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"etag"}},
			}))
			Expect(err).NotTo(HaveOccurred())
			Expect(service.last().(*testpb.UpdateBookRequest).GetUpdateMask().GetPaths()).To(BeEmpty())
		})

		It("refuses a partial update that clears a REQUIRED field its mask names", func(ctx SpecContext) {
			_, err := client.UpdateBook(ctx, connect.NewRequest(&testpb.UpdateBookRequest{
				Book:       &testpb.Book{Name: "books/dune"},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"title"}},
			}))
			Expect(violations(err)).To(Equal([]string{"book.title required"}))
		})
	})

	Describe("a unary batch", func() {
		It("reports each entry's violations under its index", func(ctx SpecContext) {
			_, err := client.BatchCreateBooks(ctx, connect.NewRequest(&testpb.BatchCreateBooksRequest{
				Parent: "shelves/a",
				Requests: []*testpb.CreateBookRequest{
					{Parent: "shelves/a", Book: &testpb.Book{Title: "Dune", Author: "Herbert"}},
					{Parent: "shelves/a", Book: &testpb.Book{Author: "Herbert"}},
				},
			}))
			Expect(violations(err)).To(Equal([]string{"requests[1].book.title required"}))
		})

		It("clears INPUT_ONLY fields from every resource in the response", func(ctx SpecContext) {
			response, err := client.BatchCreateBooks(ctx, connect.NewRequest(&testpb.BatchCreateBooksRequest{
				Parent: "shelves/a",
				Requests: []*testpb.CreateBookRequest{
					{Parent: "shelves/a", Book: &testpb.Book{Title: "Dune", Author: "Herbert"}},
					{Parent: "shelves/a", Book: &testpb.Book{Title: "Emma", Author: "Austen"}},
				},
			}))
			Expect(err).NotTo(HaveOccurred())
			for _, book := range response.Msg.GetBooks() {
				Expect(book.GetRequestToken()).To(BeEmpty())
			}
			for _, entry := range service.last().(*testpb.BatchCreateBooksRequest).GetRequests() {
				_, err := uuid.Parse(entry.GetBookId())
				Expect(err).NotTo(HaveOccurred())
			}
		})
	})

	It("passes a request that writes nothing through, clearing only INPUT_ONLY on the way out", func(ctx SpecContext) {
		response, err := client.GetBook(ctx, connect.NewRequest(&testpb.GetBookRequest{Name: "books/dune"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(response.Msg.GetName()).To(Equal("books/dune"))
		Expect(response.Msg.GetRequestToken()).To(BeEmpty())
	})

	It("clears OUTPUT_ONLY on every streamed message received and INPUT_ONLY on every one sent", func(ctx SpecContext) {
		stream := client.SyncBooks(ctx)
		for _, title := range []string{"Dune", "Emma"} {
			Expect(stream.Send(&testpb.Book{Title: title, Etag: "client-etag", RequestToken: "token"})).To(Succeed())
			book, err := stream.Receive()
			Expect(err).NotTo(HaveOccurred())
			Expect(book.GetTitle()).To(Equal(title))
			Expect(book.GetEtag()).To(Equal("server-etag"))
			Expect(book.GetRequestToken()).To(BeEmpty())

			received := service.last().(*testpb.Book)
			Expect(received.GetEtag()).To(BeEmpty())
			Expect(received.GetRequestToken()).To(Equal("token"))
		}
		Expect(stream.CloseRequest()).To(Succeed())
		Expect(stream.CloseResponse()).To(Succeed())
	})

	It("leaves a client's requests and responses untouched", func(ctx SpecContext) {
		interceptor, err := aip.NewFieldBehaviorInterceptor()
		Expect(err).NotTo(HaveOccurred())

		plain := &bookService{}
		mux := http.NewServeMux()
		mux.Handle(testpbconnect.NewBookServiceHandler(plain))
		bare := httptest.NewServer(mux)
		DeferCleanup(bare.Close)
		client := testpbconnect.NewBookServiceClient(bare.Client(), bare.URL, connect.WithInterceptors(interceptor))

		response, err := client.UpdateBook(ctx, connect.NewRequest(&testpb.UpdateBookRequest{
			Book: &testpb.Book{Name: "books/dune", Etag: "client-etag"},
		}))
		Expect(err).NotTo(HaveOccurred())
		received := plain.last().(*testpb.UpdateBookRequest)
		Expect(received.GetBook().GetEtag()).To(Equal("client-etag"))
		Expect(received.GetUpdateMask()).To(BeNil())
		Expect(response.Msg.GetRequestToken()).To(Equal("leaked"))
	})
})
