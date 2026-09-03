package aip_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/protoc-contrib/aip-go"
	testpb "github.com/protoc-contrib/aip-go/internal/testpb"
)

var _ = Describe("OrderBy", func() {
	Describe("UnmarshalString", func() {
		DescribeTable("parses valid order_by values",
			func(input string, expected []aip.OrderByField) {
				var orderBy aip.OrderBy
				Expect(orderBy.UnmarshalString(input)).To(Succeed())
				Expect(orderBy.Fields).To(Equal(expected))
			},
			Entry("empty", "", []aip.OrderByField(nil)),
			Entry("whitespace only", "   ", []aip.OrderByField(nil)),
			Entry("single field", "title", []aip.OrderByField{{Path: "title"}}),
			Entry("explicit asc", "title asc", []aip.OrderByField{{Path: "title"}}),
			Entry("explicit desc", "title desc", []aip.OrderByField{{Path: "title", Desc: true}}),
			Entry("dotted path", "author.name", []aip.OrderByField{{Path: "author.name"}}),
			Entry("multiple fields", "title, create_time desc", []aip.OrderByField{
				{Path: "title"}, {Path: "create_time", Desc: true},
			}),
			Entry("no space after comma", "title,create_time", []aip.OrderByField{
				{Path: "title"}, {Path: "create_time"},
			}),
			Entry("extra whitespace", "  title   desc ,  name  ", []aip.OrderByField{
				{Path: "title", Desc: true}, {Path: "name"},
			}),
			Entry("digits and underscores", "field_1", []aip.OrderByField{{Path: "field_1"}}),
		)

		DescribeTable("rejects invalid order_by values",
			func(input, message string) {
				var orderBy aip.OrderBy
				Expect(orderBy.UnmarshalString(input)).To(MatchError(ContainSubstring(message)))
			},
			Entry("invalid character", "title;", "invalid character"),
			Entry("parenthesis", "count(x)", "invalid character"),
			Entry("hyphen", "-title", "invalid character"),
			Entry("unknown direction", "title up", "expected 'asc' or 'desc'"),
			Entry("uppercase direction", "title DESC", "expected 'asc' or 'desc'"),
			Entry("too many parts", "title desc extra", "invalid term"),
			Entry("empty term", "title,", "empty field path"),
			Entry("leading comma", ",title", "empty field path"),
			Entry("empty path segment", "author..name", "empty segment"),
			Entry("trailing dot", "author.", "empty segment"),
			// A duplicate silently desynchronizes a key-set cursor from the
			// sort key, so it is rejected rather than deduplicated.
			Entry("duplicate field", "title, title desc", "duplicate field"),
		)

		It("replaces any previous content", func() {
			var orderBy aip.OrderBy
			Expect(orderBy.UnmarshalString("a, b")).To(Succeed())
			Expect(orderBy.UnmarshalString("c")).To(Succeed())
			Expect(orderBy.Fields).To(Equal([]aip.OrderByField{{Path: "c"}}))
		})

		It("clears fields when re-parsed as empty", func() {
			var orderBy aip.OrderBy
			Expect(orderBy.UnmarshalString("a, b")).To(Succeed())
			Expect(orderBy.UnmarshalString("")).To(Succeed())
			Expect(orderBy.Fields).To(BeEmpty())
		})
	})

	Describe("String", func() {
		DescribeTable("round-trips through UnmarshalString",
			func(input string) {
				var first aip.OrderBy
				Expect(first.UnmarshalString(input)).To(Succeed())

				var second aip.OrderBy
				Expect(second.UnmarshalString(first.String())).To(Succeed())
				Expect(second).To(Equal(first))
			},
			Entry("empty", ""),
			Entry("single", "title"),
			Entry("desc", "title desc"),
			Entry("multiple", "author.name, create_time desc, name"),
		)
	})

	Describe("Paths", func() {
		It("returns the paths in order", func() {
			var orderBy aip.OrderBy
			Expect(orderBy.UnmarshalString("author.name, create_time desc")).To(Succeed())
			Expect(orderBy.Paths()).To(Equal([]string{"author.name", "create_time"}))
		})

		It("returns nil for an empty ordering", func() {
			Expect(aip.OrderBy{}.Paths()).To(BeNil())
		})
	})

	Describe("SubFields", func() {
		It("splits a dotted path", func() {
			field := aip.OrderByField{Path: "a.b.c"}
			Expect(field.SubFields()).To(Equal([]string{"a", "b", "c"}))
		})

		It("returns nil for an empty path", func() {
			Expect(aip.OrderByField{}.SubFields()).To(BeNil())
		})
	})

	Describe("ParseOrderBy", func() {
		It("parses the request's order_by", func() {
			request := &testpb.ListBooksRequest{OrderBy: "title desc"}

			orderBy, err := aip.ParseOrderBy(request)
			Expect(err).NotTo(HaveOccurred())
			Expect(orderBy.Fields).To(Equal([]aip.OrderByField{{Path: "title", Desc: true}}))
		})

		It("returns the zero OrderBy on a parse failure", func() {
			request := &testpb.ListBooksRequest{OrderBy: "title;"}

			orderBy, err := aip.ParseOrderBy(request)
			Expect(err).To(HaveOccurred())
			Expect(orderBy).To(Equal(aip.OrderBy{}))
		})
	})

	Describe("ValidateForPaths", func() {
		It("accepts fields on the allow-list", func() {
			var orderBy aip.OrderBy
			Expect(orderBy.UnmarshalString("title, create_time desc")).To(Succeed())
			Expect(orderBy.ValidateForPaths("title", "create_time", "name")).To(Succeed())
		})

		It("rejects a field off the allow-list", func() {
			var orderBy aip.OrderBy
			Expect(orderBy.UnmarshalString("title, rating")).To(Succeed())
			Expect(orderBy.ValidateForPaths("title")).To(MatchError(ContainSubstring(`field "rating" is not orderable`)))
		})

		It("accepts an empty ordering against an empty allow-list", func() {
			Expect(aip.OrderBy{}.ValidateForPaths()).To(Succeed())
		})

		It("rejects any field when the allow-list is empty", func() {
			var orderBy aip.OrderBy
			Expect(orderBy.UnmarshalString("title")).To(Succeed())
			Expect(orderBy.ValidateForPaths()).To(HaveOccurred())
		})
	})

	Describe("ValidateForMessage", func() {
		DescribeTable("accepts fields that resolve on the message",
			func(input string) {
				var orderBy aip.OrderBy
				Expect(orderBy.UnmarshalString(input)).To(Succeed())
				Expect(orderBy.ValidateForMessage(&testpb.Book{})).To(Succeed())
			},
			Entry("scalar", "title"),
			Entry("nested", "author.name"),
			Entry("well-known message", "create_time"),
			Entry("multiple", "title desc, author.country, page_count"),
		)

		DescribeTable("rejects fields that do not",
			func(input, message string) {
				var orderBy aip.OrderBy
				Expect(orderBy.UnmarshalString(input)).To(Succeed())
				Expect(orderBy.ValidateForMessage(&testpb.Book{})).To(MatchError(ContainSubstring(message)))
			},
			Entry("unknown field", "nope", `no field "nope"`),
			Entry("unknown nested field", "author.nope", `no field "nope"`),
			Entry("through a scalar", "title.nope", "cannot traverse into scalar"),
			Entry("through a repeated field", "tags.nope", "cannot traverse into repeated"),
			Entry("through a map field", "labels.nope", "cannot traverse into map"),
		)

		It("accepts an empty ordering", func() {
			Expect(aip.OrderBy{}.ValidateForMessage(&testpb.Book{})).To(Succeed())
		})
	})
})
