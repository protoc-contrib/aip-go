package aip_test

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/protoc-contrib/aip-go"
)

// bookName mirrors the shape protoc-gen-go-aip emits. It exists to prove the
// generated shape satisfies aip.ResourceName structurally.
type bookName struct {
	PublisherID string
	BookID      string
}

func (n bookName) String() string   { return "publishers/" + n.PublisherID + "/books/" + n.BookID }
func (n bookName) FullName() string { return "//example.com/" + n.String() }
func (n bookName) Type() string     { return "example.com/Book" }
func (n bookName) Pattern() string  { return "publishers/{publisher}/books/{book}" }

func (n bookName) Validate() error {
	if n.BookID == "" {
		return errors.New("book: empty")
	}
	return nil
}

func (n bookName) MarshalText() ([]byte, error) { return []byte(n.String()), nil }

var _ = Describe("ResourceName", func() {
	It("is satisfied by the generated resource name shape", func() {
		var name aip.ResourceName = bookName{PublisherID: "p1", BookID: "b1"}

		Expect(name.String()).To(Equal("publishers/p1/books/b1"))
		Expect(name.FullName()).To(Equal("//example.com/publishers/p1/books/b1"))
		Expect(name.Type()).To(Equal("example.com/Book"))
		Expect(name.Pattern()).To(Equal("publishers/{publisher}/books/{book}"))
		Expect(name.Validate()).To(Succeed())

		text, err := name.MarshalText()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(text)).To(Equal("publishers/p1/books/b1"))
	})

	It("surfaces an invalid segment through Validate", func() {
		var name aip.ResourceName = bookName{PublisherID: "p1", BookID: ""}
		Expect(name.Validate()).To(MatchError("book: empty"))
	})
})
