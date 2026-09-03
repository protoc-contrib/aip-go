package aip

import (
	"errors"
	"fmt"
	"strings"
)

// ResourceWildcard is the AIP-159 wildcard, accepted in place of a resource ID
// to mean "across all values of this segment".
//
// See: https://google.aip.dev/159 (Reading across collections).
const ResourceWildcard = "-"

// ErrInvalidResourceName is returned when a string does not match the pattern
// it was scanned against. Map it to InvalidArgument at the RPC boundary.
var ErrInvalidResourceName = errors.New("invalid resource name")

// ResourcePattern is a compiled AIP-122 resource name pattern, such as
// "publishers/{publisher}/books/{book}".
//
// It is the machinery behind the resource name types emitted by
// protoc-gen-go-aip, not a replacement for them: generated code compiles its
// pattern once at package scope and delegates the segment walking here, so
// callers keep working with concrete `PublisherBookName` values rather than
// pattern strings.
//
//	var bookNamePattern = aip.MustCompileResourcePattern("publishers/{publisher}/books/{book}")
//
//	func ParseBookName(s string) (BookName, error) {
//	        var out BookName
//	        if err := bookNamePattern.Scan(s, &out.PublisherID, &out.BookID); err != nil {
//	                return BookName{}, err
//	        }
//	        return out, nil
//	}
//
// A compiled pattern is immutable and safe for concurrent use.
//
// See: https://google.aip.dev/122 (Resource names).
type ResourcePattern struct {
	pattern   string
	segments  []patternSegment
	variables int
}

type patternSegment struct {
	// name is the literal text for a literal segment, or the variable name
	// (without braces) for a variable segment.
	name string
	// variable reports whether this segment captures a value.
	variable bool
}

// CompileResourcePattern compiles a resource name pattern.
//
// A segment written as "{name}" captures a value; every other segment is a
// literal that must match exactly. Variable names must be unique so that error
// messages can identify a segment unambiguously.
func CompileResourcePattern(pattern string) (*ResourcePattern, error) {
	if pattern == "" {
		return nil, errors.New("compile resource pattern: empty pattern")
	}
	parts := strings.Split(pattern, "/")
	compiled := &ResourcePattern{pattern: pattern, segments: make([]patternSegment, 0, len(parts))}
	seen := make(map[string]struct{}, len(parts))
	for i, part := range parts {
		if !strings.HasPrefix(part, "{") {
			if part == "" {
				return nil, fmt.Errorf("compile resource pattern %q: empty segment %d", pattern, i)
			}
			if strings.ContainsAny(part, "{}") {
				return nil, fmt.Errorf("compile resource pattern %q: malformed segment %q", pattern, part)
			}
			compiled.segments = append(compiled.segments, patternSegment{name: part})
			continue
		}
		if !strings.HasSuffix(part, "}") {
			return nil, fmt.Errorf("compile resource pattern %q: malformed segment %q", pattern, part)
		}
		name := part[1 : len(part)-1]
		if name == "" {
			return nil, fmt.Errorf("compile resource pattern %q: empty variable name in segment %d", pattern, i)
		}
		if strings.ContainsAny(name, "{}") {
			return nil, fmt.Errorf("compile resource pattern %q: malformed segment %q", pattern, part)
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("compile resource pattern %q: duplicate variable %q", pattern, name)
		}
		seen[name] = struct{}{}
		compiled.segments = append(compiled.segments, patternSegment{name: name, variable: true})
		compiled.variables++
	}
	return compiled, nil
}

// MustCompileResourcePattern is [CompileResourcePattern] but panics on a bad
// pattern.
//
// Intended for package-scope initialization in generated code, where the
// pattern is a compile-time constant and a failure is a codegen bug rather
// than a runtime condition.
func MustCompileResourcePattern(pattern string) *ResourcePattern {
	compiled, err := CompileResourcePattern(pattern)
	if err != nil {
		panic(err)
	}
	return compiled
}

// String returns the pattern this was compiled from.
func (p *ResourcePattern) String() string {
	return p.pattern
}

// Variables returns the number of variable segments in the pattern.
func (p *ResourcePattern) Variables() int {
	return p.variables
}

// VariableNames returns the variable segment names, in pattern order.
func (p *ResourcePattern) VariableNames() []string {
	names := make([]string, 0, p.variables)
	for _, segment := range p.segments {
		if segment.variable {
			names = append(names, segment.name)
		}
	}
	return names
}

// Scan matches name against the pattern and assigns each variable segment to
// the corresponding pointer, in pattern order.
//
// The number of pointers must equal [ResourcePattern.Variables]; a mismatch is
// a programming error in the caller and is reported as such. Literal segments
// must match exactly and variable segments must be non-empty. On failure the
// pointers are left untouched.
//
// Scan walks name in place and allocates nothing.
func (p *ResourcePattern) Scan(name string, into ...*string) error {
	if len(into) != p.variables {
		return fmt.Errorf(
			"scan resource name against %q: got %d destinations, want %d",
			p.pattern, len(into), p.variables,
		)
	}
	// Count first so a length mismatch reports what the caller actually sent
	// rather than failing on whichever segment happens to differ.
	if got := strings.Count(name, "/") + 1; got != len(p.segments) {
		return fmt.Errorf(
			"%w: parse %q against %q: bad number of segments, want %d, got %d",
			ErrInvalidResourceName, name, p.pattern, len(p.segments), got,
		)
	}
	// Two walks rather than one: the first rejects, the second assigns. A
	// single walk would have to either stage the values (an allocation on
	// every parse) or write them as it goes, leaving the caller's struct
	// half-populated when a later segment turns out not to match.
	rest := name
	for i, segment := range p.segments {
		part := rest
		if i < len(p.segments)-1 {
			slash := strings.IndexByte(rest, '/')
			part, rest = rest[:slash], rest[slash+1:]
		}
		if !segment.variable {
			if part != segment.name {
				return fmt.Errorf(
					"%w: parse %q against %q: bad segment %d, want %q, got %q",
					ErrInvalidResourceName, name, p.pattern, i, segment.name, part,
				)
			}
			continue
		}
		if part == "" {
			return fmt.Errorf(
				"%w: parse %q against %q: empty value for segment %d (%s)",
				ErrInvalidResourceName, name, p.pattern, i, segment.name,
			)
		}
	}
	rest = name
	next := 0
	for i, segment := range p.segments {
		part := rest
		if i < len(p.segments)-1 {
			slash := strings.IndexByte(rest, '/')
			part, rest = rest[:slash], rest[slash+1:]
		}
		if segment.variable {
			*into[next] = part
			next++
		}
	}
	return nil
}

// Format renders the pattern with values substituted for its variable
// segments, in pattern order.
//
// The number of values must equal [ResourcePattern.Variables]; passing the
// wrong number panics, since generated callers always pass a fixed list and a
// mismatch is a codegen bug. Format does not validate the values — call
// [ResourcePattern.Validate] for that.
func (p *ResourcePattern) Format(values ...string) string {
	if len(values) != p.variables {
		panic(fmt.Sprintf(
			"format resource name against %q: got %d values, want %d",
			p.pattern, len(values), p.variables,
		))
	}
	size := len(p.segments) - 1 // the separators
	for _, segment := range p.segments {
		if !segment.variable {
			size += len(segment.name)
		}
	}
	for _, value := range values {
		size += len(value)
	}
	var builder strings.Builder
	builder.Grow(size)
	next := 0
	for i, segment := range p.segments {
		if i > 0 {
			builder.WriteByte('/')
		}
		if segment.variable {
			builder.WriteString(values[next])
			next++
			continue
		}
		builder.WriteString(segment.name)
	}
	return builder.String()
}

// Validate reports whether values are usable as the variable segments of the
// pattern: each must be non-empty and free of the '/' separator.
//
// Errors name the offending variable segment, so a caller holding a parsed
// name learns which field is at fault.
func (p *ResourcePattern) Validate(values ...string) error {
	if len(values) != p.variables {
		return fmt.Errorf(
			"validate resource name against %q: got %d values, want %d",
			p.pattern, len(values), p.variables,
		)
	}
	next := 0
	for _, segment := range p.segments {
		if !segment.variable {
			continue
		}
		value := values[next]
		next++
		if err := ValidateResourceID(value); err != nil {
			return fmt.Errorf("%s: %w", segment.name, err)
		}
	}
	return nil
}

// ValidateResourceID reports whether id is usable as a single resource ID
// segment.
//
// An ID must be non-empty and must not contain the '/' separator, which would
// silently split one segment into two.
func ValidateResourceID(id string) error {
	switch {
	case id == "":
		return errors.New("empty")
	case strings.IndexByte(id, '/') != -1:
		return errors.New("contains illegal character '/'")
	default:
		return nil
	}
}

// ContainsWildcard reports whether any of ids is the AIP-159 wildcard "-".
//
// A name containing a wildcard identifies a collection to read across rather
// than a single resource, so storage layers should treat it as a query rather
// than a lookup.
func ContainsWildcard(ids ...string) bool {
	for _, id := range ids {
		if id == ResourceWildcard {
			return true
		}
	}
	return false
}
