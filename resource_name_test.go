package aip_test

import (
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
