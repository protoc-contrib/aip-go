package aip_test

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/protoc-contrib/aip-go"
)

var _ = Describe("ResourcePattern", func() {
	Describe("CompileResourcePattern", func() {
		DescribeTable("compiles valid patterns",
			func(pattern string, variables int, names []string) {
				compiled, err := aip.CompileResourcePattern(pattern)
				Expect(err).NotTo(HaveOccurred())
				Expect(compiled.String()).To(Equal(pattern))
				Expect(compiled.Variables()).To(Equal(variables))
				Expect(compiled.VariableNames()).To(Equal(names))
			},
			Entry("single variable", "things/{thing}", 1, []string{"thing"}),
			Entry("nested", "projects/{project}/things/{thing}", 2, []string{"project", "thing"}),
			Entry("singleton, trailing literal", "projects/{project}/settings", 1, []string{"project"}),
			Entry("all literal", "projects/defaults", 0, []string{}),
			Entry("leading variable", "{organization}/items/{item}", 2, []string{"organization", "item"}),
			Entry("camelCase literal, snake_case variables", "billingAccounts/{billing_account}/logs/{log2}", 2, []string{"billing_account", "log2"}),
		)

		DescribeTable("rejects invalid patterns",
			func(pattern, message string) {
				_, err := aip.CompileResourcePattern(pattern)
				Expect(err).To(MatchError(ContainSubstring(message)))
			},
			Entry("empty", "", "empty pattern"),
			Entry("empty segment", "things//{thing}", "empty segment"),
			Entry("trailing slash", "things/{thing}/", "empty segment"),
			Entry("unclosed variable", "things/{thing", "malformed segment"),
			Entry("empty variable name", "things/{}", "empty variable name"),
			Entry("brace inside literal", "th{ings/x", "malformed segment"),
			Entry("nested braces", "things/{a{b}}", "malformed segment"),
			Entry("duplicate variable", "a/{x}/b/{x}", `duplicate variable "x"`),
			// AIP-122: collection identifiers are camelCase.
			Entry("capitalized literal", "Publishers/{id}", `literal "Publishers" is not a camelCase collection identifier`),
			Entry("snake_case literal", "book_shelves/{id}", `literal "book_shelves" is not a camelCase collection identifier`),
			Entry("kebab-case literal", "book-shelves/{id}", `literal "book-shelves" is not a camelCase collection identifier`),
			Entry("literal with a space", "a b/{id}", `literal "a b" is not a camelCase collection identifier`),
			Entry("literal starting with a digit", "1books/{id}", `literal "1books" is not a camelCase collection identifier`),
			// AIP-123: pattern variables are snake_case.
			Entry("path wildcard", "books/{book=**}", `variable name "book=**" is not snake_case`),
			Entry("variable with a space", "books/{pub lisher}", `variable name "pub lisher" is not snake_case`),
			Entry("camelCase variable", "books/{billingAccount}", `variable name "billingAccount" is not snake_case`),
			Entry("variable starting with an underscore", "books/{_book}", `variable name "_book" is not snake_case`),
			Entry("variable starting with a digit", "books/{1book}", `variable name "1book" is not snake_case`),
		)
	})

	Describe("MustCompileResourcePattern", func() {
		It("returns the compiled pattern", func() {
			Expect(aip.MustCompileResourcePattern("things/{thing}").Variables()).To(Equal(1))
		})

		It("panics on a bad pattern", func() {
			Expect(func() { aip.MustCompileResourcePattern("things/{thing") }).To(Panic())
		})
	})

	Describe("Scan", func() {
		It("assigns variable segments in pattern order", func() {
			pattern := aip.MustCompileResourcePattern("projects/{project}/things/{thing}")

			var project, thing string
			Expect(pattern.Scan("projects/p1/things/t1", &project, &thing)).To(Succeed())
			Expect(project).To(Equal("p1"))
			Expect(thing).To(Equal("t1"))
		})

		It("matches a singleton's trailing literal", func() {
			pattern := aip.MustCompileResourcePattern("projects/{project}/settings")

			var project string
			Expect(pattern.Scan("projects/p1/settings", &project)).To(Succeed())
			Expect(project).To(Equal("p1"))
		})

		It("scans an all-literal pattern", func() {
			pattern := aip.MustCompileResourcePattern("projects/defaults")
			Expect(pattern.Scan("projects/defaults")).To(Succeed())
		})

		It("accepts the wildcard as a segment value", func() {
			pattern := aip.MustCompileResourcePattern("projects/{project}/things/{thing}")

			var project, thing string
			Expect(pattern.Scan("projects/-/things/t1", &project, &thing)).To(Succeed())
			Expect(project).To(Equal("-"))
			Expect(aip.ContainsWildcard(project, thing)).To(BeTrue())
		})

		DescribeTable("rejects names that do not match",
			func(name, message string) {
				pattern := aip.MustCompileResourcePattern("projects/{project}/things/{thing}")

				var project, thing string
				err := pattern.Scan(name, &project, &thing)
				Expect(err).To(MatchError(aip.ErrInvalidResourceName))
				Expect(err).To(MatchError(ContainSubstring(message)))
			},
			Entry("too few segments", "projects/p1", "bad number of segments, want 4, got 2"),
			Entry("too many segments", "projects/p1/things/t1/x", "bad number of segments, want 4, got 5"),
			Entry("empty name", "", "bad number of segments"),
			Entry("wrong leading literal", "project/p1/things/t1", `bad segment 0, want "projects", got "project"`),
			Entry("wrong middle literal", "projects/p1/thing/t1", `bad segment 2, want "things", got "thing"`),
			Entry("empty first variable", "projects//things/t1", "empty value for segment 1 (project)"),
			Entry("empty last variable", "projects/p1/things/", "empty value for segment 3 (thing)"),
		)

		It("leaves destinations untouched when the name does not match", func() {
			pattern := aip.MustCompileResourcePattern("projects/{project}/things/{thing}")

			project, thing := "before", "before"
			// The first segment matches and the second is valid, but the
			// third literal is wrong — nothing should have been assigned.
			Expect(pattern.Scan("projects/p1/nope/t1", &project, &thing)).To(HaveOccurred())
			Expect(project).To(Equal("before"))
			Expect(thing).To(Equal("before"))
		})

		It("reports a destination count mismatch as a caller error", func() {
			pattern := aip.MustCompileResourcePattern("projects/{project}/things/{thing}")

			var project string
			err := pattern.Scan("projects/p1/things/t1", &project)
			Expect(err).To(MatchError(ContainSubstring("got 1 destinations, want 2")))
			Expect(err).NotTo(MatchError(aip.ErrInvalidResourceName))
		})

		DescribeTable("says what was wrong in a *aip.ParseError",
			func(name string, want aip.ParseError) {
				pattern := aip.MustCompileResourcePattern("projects/{project}/things/{thing}")

				var project, thing string
				err := pattern.Scan(name, &project, &thing)
				perr, ok := errors.AsType[*aip.ParseError](err)
				Expect(ok).To(BeTrue(), "%v", err)
				want.Pattern, want.Name = "projects/{project}/things/{thing}", name
				Expect(*perr).To(Equal(want))
				// and it is still the sentinel
				Expect(errors.Is(err, aip.ErrInvalidResourceName)).To(BeTrue())
			},
			Entry("the wrong number of segments", "projects/p1",
				aip.ParseError{Kind: aip.ParseSegmentCount, WantSegments: 4, GotSegments: 2}),
			Entry("a literal that does not match", "projects/p1/thing/t1",
				aip.ParseError{Kind: aip.ParseLiteral, Index: 2, Want: "things", Got: "thing"}),
			Entry("a variable with no value", "projects//things/t1",
				aip.ParseError{Kind: aip.ParseEmptyValue, Index: 1, Field: "project"}),
		)
	})

	Describe("ScanFull", func() {
		pattern := aip.MustCompileResourcePattern("projects/{project}/things/{thing}")

		It("assigns variable segments of the fully-qualified form, in pattern order", func() {
			var project, thing string
			Expect(pattern.ScanFull("//example.com/projects/p1/things/t1", "example.com", &project, &thing)).To(Succeed())
			Expect(project).To(Equal("p1"))
			Expect(thing).To(Equal("t1"))
		})

		DescribeTable("rejects a name without the prefix, against the name as given",
			func(name string) {
				var project, thing string
				err := pattern.ScanFull(name, "example.com", &project, &thing)
				perr, ok := errors.AsType[*aip.ParseError](err)
				Expect(ok).To(BeTrue(), "%v", err)
				Expect(*perr).To(Equal(aip.ParseError{
					Pattern: "projects/{project}/things/{thing}", Name: name,
					Kind: aip.ParsePrefix, Want: "//example.com/",
				}))
				Expect(err).To(MatchError(ContainSubstring(`bad prefix, want "//example.com/"`)))
				Expect(errors.Is(err, aip.ErrInvalidResourceName)).To(BeTrue())
			},
			Entry("the relative form", "projects/p1/things/t1"),
			Entry("another domain", "//other.com/projects/p1/things/t1"),
			Entry("a domain that only starts the same", "//example.comx/projects/p1/things/t1"),
			Entry("one slash", "/example.com/projects/p1/things/t1"),
		)

		It("reports a failure past the prefix against the relative part", func() {
			var project, thing string
			err := pattern.ScanFull("//example.com/projects/p1/thing/t1", "example.com", &project, &thing)
			perr, ok := errors.AsType[*aip.ParseError](err)
			Expect(ok).To(BeTrue(), "%v", err)
			Expect(perr.Name).To(Equal("projects/p1/thing/t1"))
			Expect(perr.Kind).To(Equal(aip.ParseLiteral))
		})

		It("leaves destinations untouched when the name does not match", func() {
			project, thing := "before", "before"
			Expect(pattern.ScanFull("//other.com/projects/p1/things/t1", "example.com", &project, &thing)).To(HaveOccurred())
			Expect(project).To(Equal("before"))
			Expect(thing).To(Equal("before"))
		})
	})

	Describe("InvalidValue and InvalidID", func() {
		pattern := aip.MustCompileResourcePattern("books/{book}")

		It("names the variable whose value the caller refused", func() {
			err := pattern.InvalidValue("books/seventeen", "book", errors.New("not a number"))
			Expect(*err).To(Equal(aip.ParseError{
				Pattern: "books/{book}", Name: "books/seventeen",
				Kind: aip.ParseInvalidValue, Field: "book", Reason: "not a number",
			}))
			Expect(err).To(MatchError(
				`invalid resource name: parse "books/seventeen" against "books/{book}": invalid value for segment (book): not a number`))
			Expect(errors.Is(err, aip.ErrInvalidResourceName)).To(BeTrue())
		})

		It("names the request field that held a bad id, which is not a name", func() {
			err := pattern.InvalidID("book_id", "abc", errors.New("invalid length"))
			Expect(*err).To(Equal(aip.ParseError{
				Pattern: "books/{book}", Name: "abc",
				Kind: aip.ParseInvalidID, Field: "book_id", Reason: "invalid length",
			}))
			Expect(err).To(MatchError(`parse book_id "abc": invalid length`))
		})
	})

	Describe("Format", func() {
		DescribeTable("renders the pattern",
			func(pattern string, values []string, expected string) {
				Expect(aip.MustCompileResourcePattern(pattern).Format(values...)).To(Equal(expected))
			},
			Entry("single", "things/{thing}", []string{"t1"}, "things/t1"),
			Entry("nested", "projects/{project}/things/{thing}", []string{"p1", "t1"}, "projects/p1/things/t1"),
			Entry("singleton", "projects/{project}/settings", []string{"p1"}, "projects/p1/settings"),
			Entry("all literal", "projects/defaults", nil, "projects/defaults"),
			Entry("leading variable", "{org}/items/{item}", []string{"o1", "i1"}, "o1/items/i1"),
			Entry("wildcard", "projects/{project}/things/{thing}", []string{"-", "t1"}, "projects/-/things/t1"),
		)

		It("round-trips with Scan", func() {
			pattern := aip.MustCompileResourcePattern("publishers/{publisher}/books/{book}")
			name := pattern.Format("p1", "b1")

			var publisher, book string
			Expect(pattern.Scan(name, &publisher, &book)).To(Succeed())
			Expect(pattern.Format(publisher, book)).To(Equal(name))
		})

		It("panics on the wrong number of values", func() {
			pattern := aip.MustCompileResourcePattern("things/{thing}")
			Expect(func() { pattern.Format("a", "b") }).To(Panic())
		})
	})

	Describe("Validate", func() {
		It("accepts usable values", func() {
			pattern := aip.MustCompileResourcePattern("projects/{project}/things/{thing}")
			Expect(pattern.Validate("p1", "t1")).To(Succeed())
		})

		It("accepts the wildcard", func() {
			pattern := aip.MustCompileResourcePattern("things/{thing}")
			Expect(pattern.Validate("-")).To(Succeed())
		})

		DescribeTable("names the offending segment",
			func(values []string, message string) {
				pattern := aip.MustCompileResourcePattern("projects/{project}/things/{thing}")
				Expect(pattern.Validate(values...)).To(MatchError(message))
			},
			Entry("empty first", []string{"", "t1"}, "project: empty"),
			Entry("empty second", []string{"p1", ""}, "thing: empty"),
			Entry("slash in first", []string{"a/b", "t1"}, "project: contains illegal character '/'"),
			Entry("slash in second", []string{"p1", "a/b"}, "thing: contains illegal character '/'"),
		)

		It("reports a value count mismatch", func() {
			pattern := aip.MustCompileResourcePattern("projects/{project}/things/{thing}")
			Expect(pattern.Validate("p1")).To(MatchError(ContainSubstring("got 1 values, want 2")))
		})
	})

	Describe("ValidateResourceID", func() {
		It("accepts a plain ID", func() {
			Expect(aip.ValidateResourceID("t1")).To(Succeed())
		})

		It("accepts the wildcard", func() {
			Expect(aip.ValidateResourceID(aip.ResourceWildcard)).To(Succeed())
		})

		It("rejects an empty ID", func() {
			Expect(aip.ValidateResourceID("")).To(MatchError("empty"))
		})

		It("rejects an ID containing a separator", func() {
			Expect(aip.ValidateResourceID("a/b")).To(MatchError("contains illegal character '/'"))
		})
	})

	Describe("ContainsWildcard", func() {
		It("reports a wildcard anywhere in the list", func() {
			Expect(aip.ContainsWildcard("a", "-", "b")).To(BeTrue())
			Expect(aip.ContainsWildcard("-")).To(BeTrue())
		})

		It("reports no wildcard otherwise", func() {
			Expect(aip.ContainsWildcard("a", "b")).To(BeFalse())
			Expect(aip.ContainsWildcard()).To(BeFalse())
			// A hyphen inside an ID is not the wildcard.
			Expect(aip.ContainsWildcard("a-b")).To(BeFalse())
		})
	})
})

// bookName mirrors the shape protoc-gen-go-aip emits, including the
// delegation to a package-scope compiled pattern. It exists to prove the
// generated shape satisfies aip.ResourceName structurally.
type bookName struct {
	PublisherID string
	BookID      string
}

var bookNamePattern = aip.MustCompileResourcePattern("publishers/{publisher}/books/{book}")

func parseBookName(s string) (bookName, error) {
	var out bookName
	if err := bookNamePattern.Scan(s, &out.PublisherID, &out.BookID); err != nil {
		return bookName{}, err
	}
	return out, nil
}

func (n bookName) String() string   { return bookNamePattern.Format(n.PublisherID, n.BookID) }
func (n bookName) FullName() string { return "//example.com/" + n.String() }
func (n bookName) Type() string     { return "example.com/Book" }
func (n bookName) Pattern() string  { return bookNamePattern.String() }
func (n bookName) Validate() error  { return bookNamePattern.Validate(n.PublisherID, n.BookID) }

func (n bookName) MarshalText() ([]byte, error) { return []byte(n.String()), nil }

func (n bookName) ContainsWildcard() bool {
	return aip.ContainsWildcard(n.PublisherID, n.BookID)
}

var _ = Describe("ResourceName", func() {
	It("is satisfied by the generated resource name shape", func() {
		var name aip.ResourceName = bookName{PublisherID: "p1", BookID: "b1"}

		Expect(name.String()).To(Equal("publishers/p1/books/b1"))
		Expect(name.FullName()).To(Equal("//example.com/publishers/p1/books/b1"))
		Expect(name.Type()).To(Equal("example.com/Book"))
		Expect(name.Pattern()).To(Equal("publishers/{publisher}/books/{book}"))
		Expect(name.Validate()).To(Succeed())
		Expect(name.ContainsWildcard()).To(BeFalse())

		text, err := name.MarshalText()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(text)).To(Equal("publishers/p1/books/b1"))
	})

	It("round-trips through the delegated pattern", func() {
		parsed, err := parseBookName("publishers/p1/books/b1")
		Expect(err).NotTo(HaveOccurred())
		Expect(parsed).To(Equal(bookName{PublisherID: "p1", BookID: "b1"}))
		Expect(parsed.String()).To(Equal("publishers/p1/books/b1"))
	})

	It("reports a wildcard segment through the interface", func() {
		var name aip.ResourceName = bookName{PublisherID: "-", BookID: "b1"}
		Expect(name.ContainsWildcard()).To(BeTrue())
		Expect(name.Validate()).To(Succeed())
	})

	It("surfaces an invalid segment through Validate", func() {
		var name aip.ResourceName = bookName{PublisherID: "p1", BookID: ""}
		Expect(name.Validate()).To(MatchError("book: empty"))
	})
})
