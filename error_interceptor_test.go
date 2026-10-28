package aip_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/protoc-contrib/aip-go"
	testpb "github.com/protoc-contrib/aip-go/internal/testpb"
	"github.com/protoc-contrib/aip-go/internal/testpb/testpbconnect"
)

// failingService answers GetBook with the error it was given.
type failingService struct {
	testpbconnect.UnimplementedBookServiceHandler
	err error
}

func (s *failingService) GetBook(context.Context, *testpb.GetBookRequest) (*testpb.Book, error) {
	return nil, s.err
}

// recordingHandler keeps the records it is asked to log.
type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, record)
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

func (h *recordingHandler) last() slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	Expect(h.records).NotTo(BeEmpty())
	return h.records[len(h.records)-1]
}

func (h *recordingHandler) attrs() map[string]slog.Value {
	found := map[string]slog.Value{}
	h.last().Attrs(func(attr slog.Attr) bool {
		found[attr.Key] = attr.Value
		return true
	})
	return found
}

var _ = Describe("ErrorInterceptor", func() {
	var (
		service *failingService
		logs    *recordingHandler
		client  testpbconnect.BookServiceClient
	)

	BeforeEach(func() {
		service = &failingService{}
		logs = &recordingHandler{}
	})

	// start serves the service behind an error interceptor with options.
	start := func(options ...aip.ErrorInterceptorOption) {
		mux := http.NewServeMux()
		srv := connect.NewServer(aip.NewErrorInterceptor(options...))
		testpbconnect.RegisterBookServiceHandler(srv, service)
		connecthttp.Mount(mux, srv)
		server := httptest.NewUnstartedServer(mux)
		server.EnableHTTP2 = true
		server.StartTLS()
		DeferCleanup(server.Close)
		client = testpbconnect.NewBookServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL)))
	}

	logger := func(context.Context) *slog.Logger { return slog.New(logs) }

	// detailTypes lists the types of the details err carries.
	detailTypes := func(err error) []string {
		var cerr *connect.Error
		Expect(errors.As(err, &cerr)).To(BeTrue(), "%v is not a *connect.Error", err)
		var types []string
		for _, detail := range cerr.Details() {
			types = append(types, detail.Type)
		}
		return types
	}

	It("answers a handler's error without its DebugInfo, which only the log keeps", func(ctx SpecContext) {
		cause := errors.New("the query failed")
		service.err = connect.NewError(connect.CodeNotFound, "The requested book was not found").
			WithDetail(aip.NewErrorDetail(&errdetails.ResourceInfo{ResourceName: "books/dune"})).
			WithDetail(aip.NewDebugInfo(cause)).
			WithCause(cause)
		start(aip.WithErrorLogger(logger))

		_, err := client.GetBook(ctx, &testpb.GetBookRequest{Name: "books/dune"})

		var cerr *connect.Error
		Expect(errors.As(err, &cerr)).To(BeTrue())
		Expect(cerr.Code()).To(Equal(connect.CodeNotFound))
		Expect(cerr.Message()).To(Equal("The requested book was not found"))
		Expect(detailTypes(err)).To(ConsistOf("google.rpc.ResourceInfo"))

		Expect(logs.last().Message).To(Equal("The requested book was not found"))
		Expect(logs.attrs()).To(HaveKey("debug"))
	})

	It("logs a server-side failure at error and any other at warn", func(ctx SpecContext) {
		start(aip.WithErrorLogger(logger))

		service.err = connect.NewError(connect.CodeInternal, "Unable to read")
		_, _ = client.GetBook(ctx, &testpb.GetBookRequest{Name: "books/dune"})
		Expect(logs.last().Level).To(Equal(slog.LevelError))

		service.err = connect.NewError(connect.CodeNotFound, "not found")
		_, _ = client.GetBook(ctx, &testpb.GetBookRequest{Name: "books/dune"})
		Expect(logs.last().Level).To(Equal(slog.LevelWarn))
	})

	It("sends the id of the request as a RequestInfo, when it has one", func(ctx SpecContext) {
		service.err = connect.NewError(connect.CodeNotFound, "not found")
		start(
			aip.WithErrorLogger(logger),
			aip.WithErrorRequestID(func(context.Context) string { return "req-42" }),
		)

		_, err := client.GetBook(ctx, &testpb.GetBookRequest{Name: "books/dune"})

		var cerr *connect.Error
		Expect(errors.As(err, &cerr)).To(BeTrue())
		Expect(detailTypes(err)).To(ConsistOf("google.rpc.RequestInfo"))
		info := unpack(cerr.Details()[0]).(*errdetails.RequestInfo)
		Expect(info.GetRequestId()).To(Equal("req-42"))
	})

	It("sends no RequestInfo without an id", func(ctx SpecContext) {
		service.err = connect.NewError(connect.CodeNotFound, "not found")
		start(
			aip.WithErrorLogger(logger),
			aip.WithErrorRequestID(func(context.Context) string { return "" }),
		)

		_, err := client.GetBook(ctx, &testpb.GetBookRequest{Name: "books/dune"})

		Expect(detailTypes(err)).To(BeEmpty())
	})
})

var _ = Describe("ErrorLogLevel", func() {
	DescribeTable("maps a code to the level its failure is logged at",
		func(code connect.Code, level slog.Level) {
			Expect(aip.ErrorLogLevel(code)).To(Equal(level))
		},
		Entry("Internal", connect.CodeInternal, slog.LevelError),
		Entry("Unknown", connect.CodeUnknown, slog.LevelError),
		Entry("DataLoss", connect.CodeDataLoss, slog.LevelError),
		Entry("Unavailable", connect.CodeUnavailable, slog.LevelError),
		Entry("NotFound", connect.CodeNotFound, slog.LevelWarn),
		Entry("InvalidArgument", connect.CodeInvalidArgument, slog.LevelWarn),
	)
})
