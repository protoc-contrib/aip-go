package aip_test

import (
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"

	"github.com/protoc-contrib/aip-go"
	testpb "github.com/protoc-contrib/aip-go/internal/testpb"
)

// unpack returns the message an error detail carries.
func unpack(detail *connect.ErrorDetail) proto.Message {
	Expect(detail).NotTo(BeNil())
	value, err := connectproto.UnmarshalErrorDetail(detail)
	Expect(err).NotTo(HaveOccurred())
	return value
}

var _ = Describe("NewErrorDetail", func() {
	It("packs the message as a detail of its own type", func() {
		detail := aip.NewErrorDetail(&errdetails.RetryInfo{})

		Expect(detail.Type).To(Equal("google.rpc.RetryInfo"))
		Expect(unpack(detail)).To(BeAssignableToTypeOf(&errdetails.RetryInfo{}))
	})
})

var _ = Describe("NewBadRequest", func() {
	It("names the field a *FieldError carries, with a fixed description", func() {
		err := aip.NewFieldError(&testpb.GetBookRequest{}, "name", errors.New(`parse "secret": bad segment`))

		request := unpack(aip.NewBadRequest(err)).(*errdetails.BadRequest)

		Expect(request.GetFieldViolations()).To(HaveLen(1))
		Expect(request.GetFieldViolations()[0].GetField()).To(Equal("name"))
		Expect(request.GetFieldViolations()[0].GetDescription()).To(Equal("value is invalid"), "the error's own text can carry the value")
	})

	It("finds the field through wrapping", func() {
		err := fmt.Errorf("handler: %w", aip.NewFieldError(&testpb.GetBookRequest{}, "name", errors.New("bad")))

		request := unpack(aip.NewBadRequest(err)).(*errdetails.BadRequest)

		Expect(request.GetFieldViolations()[0].GetField()).To(Equal("name"))
	})

	It("panics for an error that names no field", func() {
		Expect(func() { aip.NewBadRequest(errors.New("bad")) }).To(PanicWith(ContainSubstring("names no field")))
	})
})

var _ = Describe("NewDebugInfo", func() {
	It("carries the text of the cause and the stack from the caller", func() {
		info := unpack(aip.NewDebugInfo(errors.New("the query failed"))).(*errdetails.DebugInfo)

		Expect(info.GetDetail()).To(Equal("the query failed"))
		Expect(info.GetStackEntries()).NotTo(BeEmpty())
		Expect(info.GetStackEntries()[0]).To(ContainSubstring("error_details_test.go"), "the stack starts at the caller, not in the library")
	})
})

var _ = Describe("NewStatus", func() {
	It("is nil for a nil error", func() {
		Expect(aip.NewStatus(nil)).To(BeNil())
	})

	It("carries the code, the message and the details of a *connect.Error", func() {
		err := connect.NewError(connect.CodeNotFound, "The requested book was not found").
			WithDetail(aip.NewErrorDetail(&errdetails.ResourceInfo{ResourceName: "books/dune"})).
			WithCause(errors.New("no rows"))

		result := aip.NewStatus(err)

		Expect(result.GetCode()).To(Equal(int32(connect.CodeNotFound)))
		Expect(result.GetMessage()).To(Equal("The requested book was not found"))
		Expect(result.GetDetails()).To(HaveLen(1))
		resource := &errdetails.ResourceInfo{}
		Expect(result.GetDetails()[0].UnmarshalTo(resource)).To(Succeed())
		Expect(resource.GetResourceName()).To(Equal("books/dune"))
	})

	It("answers any other error as Internal, with a fixed message", func() {
		result := aip.NewStatus(errors.New("connection refused: db-7.internal"))

		Expect(result.GetCode()).To(Equal(int32(connect.CodeInternal)))
		Expect(result.GetMessage()).To(Equal("Unable to complete the operation at this time"))
		Expect(result.GetDetails()).To(BeEmpty())
	})

	It("keeps the DebugInfo, which the error interceptor takes out before the client sees it", func() {
		cause := errors.New("no rows")
		err := connect.NewError(connect.CodeInternal, "Unable to read").WithDetail(aip.NewDebugInfo(cause))

		result := aip.NewStatus(err)

		Expect(result.GetDetails()).To(HaveLen(1))
		Expect(result.GetDetails()[0].MessageIs(&errdetails.DebugInfo{})).To(BeTrue())
	})
})

var _ = Describe("NewEntryStatus", func() {
	It("is nil for a nil error", func() {
		Expect(aip.NewEntryStatus(nil, 0)).To(BeNil())
	})

	It("names the fields of its BadRequest under the entry", func() {
		field := aip.NewFieldError(&testpb.GetBookRequest{}, "name", errors.New("bad"))
		err := connect.NewError(connect.CodeInvalidArgument, "The request contains invalid parameters").
			WithDetail(aip.NewBadRequest(field))

		result := aip.NewEntryStatus(err, 2)

		request := &errdetails.BadRequest{}
		Expect(result.GetDetails()[0].UnmarshalTo(request)).To(Succeed())
		Expect(request.GetFieldViolations()[0].GetField()).To(Equal("requests[2].name"))
	})

	It("leaves details that are not a BadRequest as they were", func() {
		err := connect.NewError(connect.CodeNotFound, "The requested book was not found").
			WithDetail(aip.NewErrorDetail(&errdetails.ResourceInfo{ResourceName: "books/dune"}))

		result := aip.NewEntryStatus(err, 5)

		resource := &errdetails.ResourceInfo{}
		Expect(result.GetDetails()[0].UnmarshalTo(resource)).To(Succeed())
		Expect(resource.GetResourceName()).To(Equal("books/dune"))
	})
})

// namedResource is a resource name with the type and the text a spec gives it.
type namedResource struct {
	aip.ResourceName
	resourceType string
	name         string
}

func (n namedResource) Type() string   { return n.resourceType }
func (n namedResource) String() string { return n.name }

var _ = Describe("NewErrorInfo", func() {
	It("carries the reason and the domain", func() {
		info := unpack(aip.NewErrorInfo("books.example.com", "BOOK_NOT_FOUND")).(*errdetails.ErrorInfo)

		Expect(info.GetReason()).To(Equal("BOOK_NOT_FOUND"))
		Expect(info.GetDomain()).To(Equal("books.example.com"))
	})
})

var _ = Describe("NewResourceInfo", func() {
	It("carries the type and the name of the resource", func() {
		name := namedResource{resourceType: "books.example.com/Book", name: "shelves/a/books/dune"}

		info := unpack(aip.NewResourceInfo(name)).(*errdetails.ResourceInfo)

		Expect(info.GetResourceType()).To(Equal("books.example.com/Book"))
		Expect(info.GetResourceName()).To(Equal("shelves/a/books/dune"))
	})
})

var _ = Describe("NewRetryInfo", func() {
	It("carries how long to wait before a retry", func() {
		info := unpack(aip.NewRetryInfo(1500 * time.Millisecond)).(*errdetails.RetryInfo)

		Expect(info.GetRetryDelay().AsDuration()).To(Equal(1500 * time.Millisecond))
	})
})
