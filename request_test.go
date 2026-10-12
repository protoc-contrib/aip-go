package aip_test

import (
	"fmt"

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

var _ = Describe("ValidateRequired", func() {
	Describe("a create", func() {
		It("reports every unset REQUIRED field of the resource", func() {
			request := &testpb.CreateBookRequest{Parent: "shelves/1", Book: &testpb.Book{}}
			Expect(pathStrings(requiredPaths(aip.ValidateRequired(request)))).To(Equal([]string{
				"(tests.CreateBookRequest).book.title",
				"(tests.CreateBookRequest).book.author",
			}))
		})

		It("reports a set nested message's REQUIRED fields", func() {
			request := &testpb.CreateBookRequest{Book: &testpb.Book{
				Title: "Dune", Author: "Herbert", Courier: &testpb.Carrier{},
			}}
			Expect(pathStrings(requiredPaths(aip.ValidateRequired(request)))).To(Equal([]string{
				"(tests.CreateBookRequest).book.courier.name",
			}))
		})

		It("reports nothing for a complete resource", func() {
			request := &testpb.CreateBookRequest{Book: &testpb.Book{Title: "Dune", Author: "Herbert"}}
			Expect(requiredPaths(aip.ValidateRequired(request))).To(BeEmpty())
		})

		It("leaves an unset resource to the request's own required rule", func() {
			Expect(requiredPaths(aip.ValidateRequired(&testpb.CreateBookRequest{}))).To(BeNil())
		})
	})

	Describe("an update", func() {
		It("reports only the REQUIRED fields its mask names", func() {
			request := &testpb.UpdateBookRequest{
				Book:       &testpb.Book{Notes: "signed"},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes", "title"}},
			}
			Expect(pathStrings(requiredPaths(aip.ValidateRequired(request)))).To(Equal([]string{
				"(tests.UpdateBookRequest).book.title",
			}))
		})

		It("lets a partial update omit a REQUIRED field its mask doesn't name", func() {
			request := &testpb.UpdateBookRequest{
				Book:       &testpb.Book{Notes: "signed"},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
			}
			Expect(requiredPaths(aip.ValidateRequired(request))).To(BeEmpty())
		})

		It("reads an unset mask as naming nothing, not as a create", func() {
			request := &testpb.UpdateBookRequest{Book: &testpb.Book{}}
			Expect(requiredPaths(aip.ValidateRequired(request))).To(BeEmpty())
		})
	})

	Describe("a batch", func() {
		It("checks each create entry under requests[i]", func() {
			request := &testpb.BatchCreateBooksRequest{Requests: []*testpb.CreateBookRequest{
				{Book: &testpb.Book{Title: "Dune", Author: "Herbert"}},
				{Book: &testpb.Book{Author: "Le Guin"}},
			}}
			Expect(pathStrings(requiredPaths(aip.ValidateRequired(request)))).To(Equal([]string{
				"(tests.BatchCreateBooksRequest).requests[1].book.title",
			}))
		})

		It("checks each update entry against its own mask", func() {
			request := &testpb.BatchUpdateBooksRequest{Requests: []*testpb.UpdateBookRequest{
				{Book: &testpb.Book{}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}}},
				{Book: &testpb.Book{}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"author"}}},
			}}
			Expect(pathStrings(requiredPaths(aip.ValidateRequired(request)))).To(Equal([]string{
				"(tests.BatchUpdateBooksRequest).requests[1].book.author",
			}))
		})
	})

	It("ignores a request that writes no resource", func() {
		Expect(requiredPaths(aip.ValidateRequired(&testpb.GetBookRequest{}))).To(BeNil())
		Expect(requiredPaths(aip.ValidateRequired(&testpb.UpdateShipmentRequest{Shipment: &testpb.Shipment{}}))).To(BeNil())
	})

	It("yields nil for a nil or typed-nil request", func() {
		Expect(requiredPaths(aip.ValidateRequired(nil))).To(BeNil())
		Expect(requiredPaths(aip.ValidateRequired((*testpb.CreateBookRequest)(nil)))).To(BeNil())
	})

	It("reads a dynamic request as a generated one", func() {
		request := dynamic(&testpb.BatchUpdateBooksRequest{Requests: []*testpb.UpdateBookRequest{
			{Book: &testpb.Book{}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"title"}}},
		}})
		Expect(pathStrings(requiredPaths(aip.ValidateRequired(request)))).To(Equal([]string{
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

// sequence returns a newID that yields "id-1", "id-2", … on each call.
func sequence() func() string {
	n := 0
	return func() string {
		n++
		return fmt.Sprintf("id-%d", n)
	}
}

var _ = Describe("RequiredError", func() {
	It("lists the unset fields, dotted, in its message", func() {
		request := &testpb.BatchCreateBooksRequest{Requests: []*testpb.CreateBookRequest{
			{Book: &testpb.Book{Title: "Dune", Author: "Herbert"}},
			{Book: &testpb.Book{Author: "Le Guin"}},
		}}
		Expect(aip.ValidateRequired(request)).To(MatchError("required fields are unset: requests[1].book.title"))
	})

	It("is nil when nothing is unset, not a typed nil", func() {
		var err error = aip.ValidateRequired(&testpb.GetBookRequest{})
		Expect(err).To(BeNil())
	})
})

var _ = Describe("ImmutableError", func() {
	It("lists the changed fields, dotted, in its message", func() {
		stored := &testpb.Shipment{CarrierCode: "DHL"}
		update := &testpb.Shipment{CarrierCode: "UPS"}
		Expect(aip.ValidateImmutable(stored, update)).To(MatchError("immutable fields cannot change: carrier_code"))
	})
})

var _ = Describe("ImplyResourceName", func() {
	It("assigns an omitted id and names the resource from its pattern", func() {
		request := &testpb.CreateBookRequest{Parent: "shelves/1", Book: &testpb.Book{Title: "Dune"}}
		aip.ImplyResourceName(request, sequence())
		Expect(request.GetBookId()).To(Equal("id-1"))
		Expect(request.GetBook().GetName()).To(Equal("books/id-1"))
	})

	It("keeps an id the client sent, as sent", func() {
		request := &testpb.CreateBookRequest{BookId: "dune", Book: &testpb.Book{}}
		aip.ImplyResourceName(request, sequence())
		Expect(request.GetBookId()).To(Equal("dune"))
		Expect(request.GetBook().GetName()).To(Equal("books/dune"))
	})

	It("replaces a name the client sent", func() {
		request := &testpb.CreateBookRequest{BookId: "dune", Book: &testpb.Book{Name: "books/other"}}
		aip.ImplyResourceName(request, sequence())
		Expect(request.GetBook().GetName()).To(Equal("books/dune"))
	})

	It("names a nested resource under the request's parent and copies the parent onto it", func() {
		request := &testpb.CreateChapterRequest{Parent: "books/dune", Chapter: &testpb.Chapter{Title: "One"}}
		aip.ImplyResourceName(request, sequence())
		Expect(request.GetChapterId()).To(Equal("id-1"))
		Expect(request.GetChapter().GetName()).To(Equal("books/dune/chapters/id-1"))
		Expect(request.GetChapter().GetParent()).To(Equal("books/dune"))
	})

	It("gives each create entry of a batch its own id", func() {
		request := &testpb.BatchCreateBooksRequest{Requests: []*testpb.CreateBookRequest{
			{Book: &testpb.Book{}},
			{BookId: "dune", Book: &testpb.Book{}},
			{Book: &testpb.Book{}},
		}}
		aip.ImplyResourceName(request, sequence())
		Expect(request.GetRequests()[0].GetBook().GetName()).To(Equal("books/id-1"))
		Expect(request.GetRequests()[1].GetBook().GetName()).To(Equal("books/dune"))
		Expect(request.GetRequests()[2].GetBook().GetName()).To(Equal("books/id-2"))
	})

	It("leaves a create without a resource alone, minting no id", func() {
		request := &testpb.CreateBookRequest{Parent: "shelves/1"}
		aip.ImplyResourceName(request, func() string {
			Fail("newID was called")
			return ""
		})
		Expect(request.GetBookId()).To(BeEmpty())
	})

	It("leaves an update's name alone: it selects the target", func() {
		request := &testpb.UpdateBookRequest{Book: &testpb.Book{Name: "books/dune"}}
		aip.ImplyResourceName(request, sequence())
		Expect(request.GetBook().GetName()).To(Equal("books/dune"))
	})

	It("leaves a create without a `<resource>_id` field alone", func() {
		request := &testpb.ImportBookRequest{Book: &testpb.Book{Name: "books/imported"}}
		aip.ImplyResourceName(request, sequence())
		Expect(request.GetBook().GetName()).To(Equal("books/imported"))
	})

	It("leaves any other request, and a nil one, alone", func() {
		request := &testpb.GetBookRequest{Name: "books/dune"}
		aip.ImplyResourceName(request, sequence())
		Expect(request.GetName()).To(Equal("books/dune"))
		Expect(func() { aip.ImplyResourceName(nil, sequence()) }).NotTo(Panic())
		Expect(func() { aip.ImplyResourceName((*testpb.CreateBookRequest)(nil), sequence()) }).NotTo(Panic())
	})

	It("names a dynamic request as a generated one", func() {
		request := dynamic(&testpb.CreateChapterRequest{Parent: "books/dune", Chapter: &testpb.Chapter{}})
		aip.ImplyResourceName(request, sequence())
		typed := &testpb.CreateChapterRequest{}
		data, err := proto.Marshal(request)
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Unmarshal(data, typed)).To(Succeed())
		Expect(typed.GetChapterId()).To(Equal("id-1"))
		Expect(typed.GetChapter().GetName()).To(Equal("books/dune/chapters/id-1"))
		Expect(typed.GetChapter().GetParent()).To(Equal("books/dune"))
	})
})

var _ = Describe("ImplyRequest", func() {
	It("names a create's resource, so validation sees the server's id", func() {
		request := &testpb.CreateBookRequest{Book: &testpb.Book{Title: "Dune", Author: "Herbert"}}
		aip.ImplyRequest(request, sequence())
		Expect(request.GetBook().GetName()).To(Equal("books/id-1"))
		Expect(request.GetBookId()).To(Equal("id-1"))
		Expect(aip.ValidateRequired(request)).To(Succeed())
	})

	It("implies an update's omitted mask and leaves its name alone", func() {
		request := &testpb.UpdateBookRequest{Book: &testpb.Book{Name: "books/dune", Notes: "worn"}}
		aip.ImplyRequest(request, sequence())
		Expect(request.GetBook().GetName()).To(Equal("books/dune"))
		Expect(request.GetUpdateMask().GetPaths()).To(Equal([]string{"notes"}))
		Expect(aip.ValidateRequired(request)).To(Succeed())
	})

	It("implies both across a batch's entries", func() {
		creates := &testpb.BatchCreateBooksRequest{Requests: []*testpb.CreateBookRequest{{Book: &testpb.Book{}}}}
		aip.ImplyRequest(creates, sequence())
		Expect(creates.GetRequests()[0].GetBook().GetName()).To(Equal("books/id-1"))

		updates := &testpb.BatchUpdateBooksRequest{Requests: []*testpb.UpdateBookRequest{
			{Book: &testpb.Book{Name: "books/a", Title: "A"}},
			{Book: &testpb.Book{Name: "books/b", Notes: "n"}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"title"}}},
		}}
		aip.ImplyRequest(updates, sequence())
		Expect(updates.GetRequests()[0].GetUpdateMask().GetPaths()).To(Equal([]string{"title"}))
		Expect(updates.GetRequests()[1].GetUpdateMask().GetPaths()).To(Equal([]string{"title"}))
	})
})
