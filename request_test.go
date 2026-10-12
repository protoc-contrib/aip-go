package aip_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protopath"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/protoc-contrib/aip-go"
	testpb "github.com/protoc-contrib/aip-go/internal/testpb"
)

// pathStrings renders paths as protopath does.
func pathStrings(paths []protopath.Path) []string {
	rendered := make([]string, 0, len(paths))
	for _, path := range paths {
		rendered = append(rendered, path.String())
	}
	return rendered
}

// dynamic returns message as a dynamicpb message of the same type.
func dynamic(message proto.Message) *dynamicpb.Message {
	data, err := proto.Marshal(message)
	Expect(err).NotTo(HaveOccurred())
	copied := dynamicpb.NewMessage(message.ProtoReflect().Descriptor())
	Expect(proto.Unmarshal(data, copied)).To(Succeed())
	return copied
}

var _ = Describe("MissingRequiredRequestPaths", func() {
	Describe("a create", func() {
		It("reports every unset REQUIRED field of the resource", func() {
			request := &testpb.CreateBookRequest{Parent: "shelves/1", Book: &testpb.Book{}}
			Expect(pathStrings(aip.MissingRequiredRequestPaths(request))).To(Equal([]string{
				"(tests.CreateBookRequest).book.title",
				"(tests.CreateBookRequest).book.author",
			}))
		})

		It("reports a set nested message's REQUIRED fields", func() {
			request := &testpb.CreateBookRequest{Book: &testpb.Book{
				Title: "Dune", Author: "Herbert", Courier: &testpb.Carrier{},
			}}
			Expect(pathStrings(aip.MissingRequiredRequestPaths(request))).To(Equal([]string{
				"(tests.CreateBookRequest).book.courier.name",
			}))
		})

		It("reports nothing for a complete resource", func() {
			request := &testpb.CreateBookRequest{Book: &testpb.Book{Title: "Dune", Author: "Herbert"}}
			Expect(aip.MissingRequiredRequestPaths(request)).To(BeEmpty())
		})

		It("leaves an unset resource to the request's own required rule", func() {
			Expect(aip.MissingRequiredRequestPaths(&testpb.CreateBookRequest{})).To(BeNil())
		})
	})

	Describe("an update", func() {
		It("reports only the REQUIRED fields its mask names", func() {
			request := &testpb.UpdateBookRequest{
				Book:       &testpb.Book{Notes: "signed"},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes", "title"}},
			}
			Expect(pathStrings(aip.MissingRequiredRequestPaths(request))).To(Equal([]string{
				"(tests.UpdateBookRequest).book.title",
			}))
		})

		It("lets a partial update omit a REQUIRED field its mask doesn't name", func() {
			request := &testpb.UpdateBookRequest{
				Book:       &testpb.Book{Notes: "signed"},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
			}
			Expect(aip.MissingRequiredRequestPaths(request)).To(BeEmpty())
		})

		It("reads an unset mask as naming nothing, not as a create", func() {
			request := &testpb.UpdateBookRequest{Book: &testpb.Book{}}
			Expect(aip.MissingRequiredRequestPaths(request)).To(BeEmpty())
		})
	})

	Describe("a batch", func() {
		It("checks each create entry under requests[i]", func() {
			request := &testpb.BatchCreateBooksRequest{Requests: []*testpb.CreateBookRequest{
				{Book: &testpb.Book{Title: "Dune", Author: "Herbert"}},
				{Book: &testpb.Book{Author: "Le Guin"}},
			}}
			Expect(pathStrings(aip.MissingRequiredRequestPaths(request))).To(Equal([]string{
				"(tests.BatchCreateBooksRequest).requests[1].book.title",
			}))
		})

		It("checks each update entry against its own mask", func() {
			request := &testpb.BatchUpdateBooksRequest{Requests: []*testpb.UpdateBookRequest{
				{Book: &testpb.Book{}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}}},
				{Book: &testpb.Book{}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"author"}}},
			}}
			Expect(pathStrings(aip.MissingRequiredRequestPaths(request))).To(Equal([]string{
				"(tests.BatchUpdateBooksRequest).requests[1].book.author",
			}))
		})
	})

	It("ignores a request that writes no resource", func() {
		Expect(aip.MissingRequiredRequestPaths(&testpb.GetBookRequest{})).To(BeNil())
		Expect(aip.MissingRequiredRequestPaths(&testpb.UpdateShipmentRequest{Shipment: &testpb.Shipment{}})).To(BeNil())
	})

	It("yields nil for a nil or typed-nil request", func() {
		Expect(aip.MissingRequiredRequestPaths(nil)).To(BeNil())
		Expect(aip.MissingRequiredRequestPaths((*testpb.CreateBookRequest)(nil))).To(BeNil())
	})

	It("reads a dynamic request as a generated one", func() {
		request := dynamic(&testpb.BatchUpdateBooksRequest{Requests: []*testpb.UpdateBookRequest{
			{Book: &testpb.Book{}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"title"}}},
		}})
		Expect(pathStrings(aip.MissingRequiredRequestPaths(request))).To(Equal([]string{
			"(tests.BatchUpdateBooksRequest).requests[0].book.title",
		}))
	})
})

var _ = Describe("ImplyUpdateMask", func() {
	It("implies the mask of an update that omitted it", func() {
		request := &testpb.UpdateBookRequest{Book: &testpb.Book{Name: "books/1", Title: "Dune", Notes: "signed"}}
		aip.ImplyUpdateMask(request)
		Expect(request.GetUpdateMask().GetPaths()).To(Equal([]string{"title", "notes"}))
	})

	It("treats a mask with no paths as omitted", func() {
		request := &testpb.UpdateBookRequest{
			Book:       &testpb.Book{Title: "Dune"},
			UpdateMask: &fieldmaskpb.FieldMask{},
		}
		aip.ImplyUpdateMask(request)
		Expect(request.GetUpdateMask().GetPaths()).To(Equal([]string{"title"}))
	})

	It("keeps a mask the client sent", func() {
		request := &testpb.UpdateBookRequest{
			Book:       &testpb.Book{Title: "Dune", Notes: "signed"},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
		}
		aip.ImplyUpdateMask(request)
		Expect(request.GetUpdateMask().GetPaths()).To(Equal([]string{"notes"}))
	})

	It("leaves an update without a resource alone", func() {
		request := &testpb.UpdateBookRequest{}
		aip.ImplyUpdateMask(request)
		Expect(request.GetUpdateMask()).To(BeNil())
	})

	It("implies each batch entry's mask on its own", func() {
		request := &testpb.BatchUpdateBooksRequest{Requests: []*testpb.UpdateBookRequest{
			{Book: &testpb.Book{Title: "Dune"}},
			{Book: &testpb.Book{Author: "Le Guin"}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}}},
		}}
		aip.ImplyUpdateMask(request)
		Expect(request.GetRequests()[0].GetUpdateMask().GetPaths()).To(Equal([]string{"title"}))
		Expect(request.GetRequests()[1].GetUpdateMask().GetPaths()).To(Equal([]string{"notes"}))
	})

	It("leaves a create untouched", func() {
		request := &testpb.CreateBookRequest{Book: &testpb.Book{Title: "Dune"}}
		before := proto.Clone(request)
		aip.ImplyUpdateMask(request)
		Expect(proto.Equal(request, before)).To(BeTrue())
	})

	It("does nothing for a nil or typed-nil request", func() {
		Expect(func() { aip.ImplyUpdateMask(nil) }).NotTo(Panic())
		Expect(func() { aip.ImplyUpdateMask((*testpb.UpdateBookRequest)(nil)) }).NotTo(Panic())
	})

	It("implies the mask of a dynamic request", func() {
		request := dynamic(&testpb.UpdateBookRequest{Book: &testpb.Book{Author: "Herbert"}})
		aip.ImplyUpdateMask(request)

		typed := &testpb.UpdateBookRequest{}
		data, err := proto.Marshal(request)
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Unmarshal(data, typed)).To(Succeed())
		Expect(typed.GetUpdateMask().GetPaths()).To(Equal([]string{"author"}))
	})
})
