package aip

import (
	"errors"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	testpb "github.com/protoc-contrib/aip-go/internal/testpb"
)

// failingValidator is a protovalidate.Validator whose rules fail to compile.
type failingValidator struct{ err error }

func (v failingValidator) Validate(proto.Message, ...protovalidate.ValidationOption) error {
	return v.err
}

var _ = Describe("FieldBehaviorInterceptor validation", func() {
	It("reports a rule that fails to compile as Internal, without its diagnostic", func() {
		cause := &protovalidate.CompilationError{}
		err := validate(failingValidator{err: cause}, &testpb.UpdateBookRequest{Book: &testpb.Book{Name: "books/dune"}})

		var cerr *connect.Error
		Expect(errors.As(err, &cerr)).To(BeTrue())
		Expect(cerr.Code()).To(Equal(connect.CodeInternal))
		Expect(cerr.Message()).To(Equal("validation failed"))
		Expect(cerr.Details()).To(BeEmpty())

		var compilation *protovalidate.CompilationError
		Expect(errors.As(err, &compilation)).To(BeTrue(), "the diagnostic stays reachable for the log")
	})
})
