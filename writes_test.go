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

// writes collects what aip.ResourceWrites yields for request.
func writes(request proto.Message) []aip.ResourceWrite {
	var collected []aip.ResourceWrite
	for write := range aip.ResourceWrites(request) {
		collected = append(collected, write)
	}
	return collected
}

// writePaths renders each write's path, dotted.
func writePaths(collected []aip.ResourceWrite) []string {
	paths := make([]protopath.Path, 0, len(collected))
	for _, write := range collected {
		paths = append(paths, write.Path)
	}
	return dotted(paths)
}

// dynamicCopy returns message as a dynamicpb message of the same type.
func dynamicCopy(message proto.Message) *dynamicpb.Message {
	data, err := proto.Marshal(message)
	Expect(err).NotTo(HaveOccurred())
	copied := dynamicpb.NewMessage(message.ProtoReflect().Descriptor())
	Expect(proto.Unmarshal(data, copied)).To(Succeed())
	return copied
}

var _ = Describe("ResourceWrites", func() {
	It("yields a create's resource with Create set", func() {
		request := &testpb.CreateBookRequest{Parent: "shelves/a", BookId: "dune", Book: &testpb.Book{Title: "Dune"}}
		collected := writes(request)
		Expect(writePaths(collected)).To(Equal([]string{"book"}))
		Expect(collected[0].Create).NotTo(BeNil())
		Expect(collected[0].Update).To(BeNil())
		Expect(collected[0].Create.ID()).To(Equal("dune"))
		Expect(collected[0].Resource).To(BeIdenticalTo(request.Book))
	})

	It("sets a create's id, leaving the resource's name and parent to the server", func() {
		request := &testpb.CreateChapterRequest{Parent: "books/dune", Chapter: &testpb.Chapter{Name: "sent"}}
		for write := range aip.ResourceWrites(request) {
			write.Create.SetID("one")
		}
		Expect(request.ChapterId).To(Equal("one"))
		Expect(request.Chapter.Name).To(Equal("sent"))
		Expect(request.Chapter.Parent).To(BeEmpty())
	})

	It("leaves a create without an id field alone", func() {
		request := &testpb.ImportBookRequest{Book: &testpb.Book{Name: "books/sent"}}
		collected := writes(request)
		Expect(collected).To(HaveLen(1))
		collected[0].Create.SetID("ignored")
		Expect(collected[0].Create.ID()).To(BeEmpty())
		Expect(request.Book.Name).To(Equal("books/sent"))
	})

	It("yields an update's resource with Update set, an unset mask reading empty", func() {
		request := &testpb.UpdateBookRequest{Book: &testpb.Book{Name: "books/dune"}}
		collected := writes(request)
		Expect(collected).To(HaveLen(1))
		Expect(collected[0].Create).To(BeNil())
		Expect(collected[0].Update.Mask().GetPaths()).To(BeEmpty())

		collected[0].Update.SetMask(&fieldmaskpb.FieldMask{Paths: []string{"title"}})
		Expect(request.UpdateMask.GetPaths()).To(Equal([]string{"title"}))
		Expect(collected[0].Update.Mask().GetPaths()).To(Equal([]string{"title"}))
	})

	It("yields each batch entry under its index", func() {
		request := &testpb.BatchUpdateBooksRequest{Requests: []*testpb.UpdateBookRequest{
			{Book: &testpb.Book{}},
			{},
			{Book: &testpb.Book{}},
		}}
		Expect(writePaths(writes(request))).To(Equal([]string{"requests[0].book", "requests[2].book"}))
	})

	It("yields nothing for a request that writes no resource", func() {
		Expect(writes(&testpb.GetBookRequest{Name: "books/dune"})).To(BeEmpty())
		Expect(writes(&testpb.CreateBookRequest{})).To(BeEmpty())
		Expect(writes(nil)).To(BeEmpty())
		Expect(writes((*testpb.CreateBookRequest)(nil))).To(BeEmpty())
	})

	It("walks a dynamic message as it walks a generated one", func() {
		request := dynamicCopy(&testpb.BatchCreateBooksRequest{Requests: []*testpb.CreateBookRequest{
			{Parent: "shelves/a", Book: &testpb.Book{}},
		}})
		for write := range aip.ResourceWrites(request) {
			write.Create.SetID("dune")
		}
		var typed testpb.BatchCreateBooksRequest
		data, err := proto.Marshal(request)
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Unmarshal(data, &typed)).To(Succeed())
		Expect(typed.Requests[0].BookId).To(Equal("dune"))

		update := dynamicCopy(&testpb.UpdateBookRequest{Book: &testpb.Book{}})
		for write := range aip.ResourceWrites(update) {
			write.Update.SetMask(&fieldmaskpb.FieldMask{Paths: []string{"title"}})
			Expect(write.Update.Mask().GetPaths()).To(Equal([]string{"title"}))
		}
	})
})
