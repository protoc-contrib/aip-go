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
//
// The error a scan returns is a [*ParseError], which says what was wrong and
// is this error too: errors.Is(err, ErrInvalidResourceName) holds for it.
var ErrInvalidResourceName = errors.New("invalid resource name")

// ParseErrorKind says what was wrong with a resource name.
type ParseErrorKind int

const (
	// ParseSegmentCount is a name with the wrong number of "/"-separated
	// segments: [ParseError.WantSegments] against [ParseError.GotSegments].
	ParseSegmentCount ParseErrorKind = iota + 1
	// ParseLiteral is a literal segment that does not match: segment
	// [ParseError.Index] is [ParseError.Got], and the pattern requires
	// [ParseError.Want].
	ParseLiteral
	// ParseEmptyValue is a variable segment with no value: segment
	// [ParseError.Index], the variable [ParseError.Field].
	ParseEmptyValue
	// ParsePrefix is a fully-qualified name that does not start with
	// "//{domain}/", which is [ParseError.Want]. Only [ResourcePattern.ScanFull]
	// produces it; the relative form has no prefix to get wrong.
	ParsePrefix
	// ParseInvalidValue is a variable segment whose value is not valid for the
	// type that segment is declared to hold: the variable [ParseError.Field],
	// and why in [ParseError.Reason]. The caller raises it with
	// [ResourcePattern.InvalidValue]; the scan has no opinion about anything
	// beyond the separator.
	ParseInvalidValue
	// ParseInvalidID is a request field holding a bare id, not a name -- a
	// create's `<resource>_id` -- whose value is not one: [ParseError.Field] is
	// the request field, [ParseError.Name] the id, and why in
	// [ParseError.Reason]. The caller raises it with [ResourcePattern.InvalidID].
	ParseInvalidID
)

// ParseError reports why a string does not parse as a resource name of a
// pattern: what every [ResourcePattern.Scan] and [ResourcePattern.ScanFull]
// failure is, so a caller can tell a name with the wrong shape from one with a
// bad literal without reading the message.
//
//	var perr *aip.ParseError
//	if errors.As(err, &perr) && perr.Kind == aip.ParseSegmentCount { ... }
//
// It is also [ErrInvalidResourceName]. Map it to InvalidArgument at the RPC
// boundary. Which of the fields below carry a value depends on Kind.
type ParseError struct {
	// Pattern is the pattern the name was parsed against.
	Pattern string
	// Name is the name that failed to parse, or for [ParseInvalidID] the id.
	Name string
	// Kind is what was wrong with it.
	Kind ParseErrorKind
	// Index is the segment's position in the pattern: [ParseLiteral] and
	// [ParseEmptyValue].
	Index int
	// Want is the literal the pattern requires for [ParseLiteral], and the
	// prefix a fully-qualified name must start with for [ParsePrefix].
	Want string
	// Got is what a [ParseLiteral] segment has instead.
	Got string
	// WantSegments and GotSegments are the number of segments the pattern has
	// and the name has: [ParseSegmentCount].
	WantSegments, GotSegments int
	// Field is the variable's name for [ParseEmptyValue] and
	// [ParseInvalidValue], and the request field for [ParseInvalidID].
	Field string
	// Reason is what the conversion said was wrong, for [ParseInvalidValue]
	// and [ParseInvalidID].
	Reason string
}

// Error describes the failure; for every kind but [ParseInvalidID] it starts
// with the text of [ErrInvalidResourceName].
func (e *ParseError) Error() string {
	// A bare id is not a name, so it is not parsed against the pattern.
	if e.Kind == ParseInvalidID {
		return fmt.Sprintf("parse %s %q: %s", e.Field, e.Name, e.Reason)
	}
	head := fmt.Sprintf("%v: parse %q against %q: ", ErrInvalidResourceName, e.Name, e.Pattern)
	switch e.Kind {
	case ParseSegmentCount:
		return head + fmt.Sprintf("bad number of segments, want %d, got %d", e.WantSegments, e.GotSegments)
	case ParseLiteral:
		return head + fmt.Sprintf("bad segment %d, want %q, got %q", e.Index, e.Want, e.Got)
	case ParseEmptyValue:
		return head + fmt.Sprintf("empty value for segment %d (%s)", e.Index, e.Field)
	case ParsePrefix:
		return head + fmt.Sprintf("bad prefix, want %q", e.Want)
	case ParseInvalidValue:
		return head + fmt.Sprintf("invalid value for segment (%s): %s", e.Field, e.Reason)
	default:
		return head + "unknown failure"
	}
}

// Is reports a [ParseError] as [ErrInvalidResourceName], so callers that
// matched the sentinel keep working.
func (e *ParseError) Is(target error) bool { return target == ErrInvalidResourceName }

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
//
// The spelling is checked too, as AIP-122 and AIP-123 ask: a literal must be a
// camelCase collection identifier ("books", "billingAccounts") and a variable
// name must be snake_case ("book", "billing_account").
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
			if !isCollectionID(part) {
				return nil, fmt.Errorf("compile resource pattern %q: literal %q is not a camelCase collection identifier", pattern, part)
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
		if !isVariableName(name) {
			return nil, fmt.Errorf("compile resource pattern %q: variable name %q is not snake_case", pattern, name)
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

// isCollectionID reports whether literal is a camelCase collection identifier,
// as AIP-122 asks: a lowercase ASCII letter followed by ASCII letters and
// digits.
func isCollectionID(literal string) bool {
	for i, c := range literal {
		switch {
		case 'a' <= c && c <= 'z':
		case i > 0 && ('A' <= c && c <= 'Z' || '0' <= c && c <= '9'):
		default:
			return false
		}
	}
	return literal != ""
}

// isVariableName reports whether name is a snake_case pattern variable, as
// AIP-123 asks: a lowercase ASCII letter followed by lowercase letters, digits
// and underscores.
func isVariableName(name string) bool {
	for i, c := range name {
		switch {
		case 'a' <= c && c <= 'z':
		case i > 0 && ('0' <= c && c <= '9' || c == '_'):
		default:
			return false
		}
	}
	return name != ""
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
		return &ParseError{
			Pattern: p.pattern, Name: name, Kind: ParseSegmentCount,
			WantSegments: len(p.segments), GotSegments: got,
		}
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
				return &ParseError{
					Pattern: p.pattern, Name: name, Kind: ParseLiteral,
					Index: i, Want: segment.name, Got: part,
				}
			}
			continue
		}
		if part == "" {
			return &ParseError{
				Pattern: p.pattern, Name: name, Kind: ParseEmptyValue,
				Index: i, Field: segment.name,
			}
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

// ScanFull matches the fully-qualified form of name -- "//{domain}/" followed
// by the pattern -- and assigns each variable segment to the corresponding
// pointer, in pattern order, as [ResourcePattern.Scan] does.
//
// domain is the service name, which by AIP-123 is the part of a resource type
// before the "/": "example.com" for "example.com/Book". The relative form is
// the one that travels in a name field; this is for the fully-qualified form
// that appears in cross-service references.
//
// A name without the prefix is a [ParsePrefix] error, reported against the
// name as given; a failure past it reports the relative part it scanned. Like
// Scan, it allocates nothing when the name matches.
func (p *ResourcePattern) ScanFull(name, domain string, into ...*string) error {
	rest, ok := strings.CutPrefix(name, "//")
	if ok {
		rest, ok = strings.CutPrefix(rest, domain)
	}
	if ok {
		rest, ok = strings.CutPrefix(rest, "/")
	}
	if !ok {
		return &ParseError{Pattern: p.pattern, Name: name, Kind: ParsePrefix, Want: "//" + domain + "/"}
	}
	return p.Scan(rest, into...)
}

// InvalidValue returns the [ParseInvalidValue] error for variable of name,
// whose value a caller's conversion refused for reason: a segment that has to
// be a number and is not. The scan itself has no opinion about a value beyond
// the separator.
func (p *ResourcePattern) InvalidValue(name, variable string, reason error) *ParseError {
	return &ParseError{Pattern: p.pattern, Name: name, Kind: ParseInvalidValue, Field: variable, Reason: reason.Error()}
}

// InvalidID returns the [ParseInvalidID] error for the request field field,
// which holds the bare id id of this pattern's resource -- a create's
// `<resource>_id` (AIP-133) -- and should not have: `parse book_id "abc":
// reason`. It names the field rather than a segment, since the field is what
// the caller sent.
func (p *ResourcePattern) InvalidID(field, id string, reason error) *ParseError {
	return &ParseError{Pattern: p.pattern, Name: id, Kind: ParseInvalidID, Field: field, Reason: reason.Error()}
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
		switch {
		case value == "":
			return fmt.Errorf("%s: empty", segment.name)
		case strings.IndexByte(value, '/') != -1:
			return fmt.Errorf("%s: contains illegal character '/'", segment.name)
		}
	}
	return nil
}

// ResourceName is the behavior shared by every resource name type emitted by
// protoc-gen-go-aip.
//
// It exists for code that must handle a resource name without knowing which
// one it is — middleware that logs, authorizes or audits whatever `name` a
// request carries. Code that knows the type should use it directly:
// ParseBookName gives a BookName with a typed Parent(), which this interface
// deliberately cannot express.
//
// Generated types satisfy this structurally, so a generated file needs no
// import of this package to conform. UnmarshalText is excluded because it
// requires a pointer receiver, which would exclude the value types generated
// code produces.
type ResourceName interface {
	// String returns the relative resource name, e.g. "publishers/p1/books/b1".
	String() string
	// FullName returns the fully-qualified name, e.g. "//example.com/publishers/p1/books/b1".
	FullName() string
	// MarshalText emits the relative resource name.
	MarshalText() ([]byte, error)
	// Type returns the AIP resource type, e.g. "example.com/Book".
	Type() string
	// Pattern returns the resource name pattern, e.g. "publishers/{publisher}/books/{book}".
	Pattern() string
	// Validate reports whether every variable segment is usable.
	Validate() error
}
