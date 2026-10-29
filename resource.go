package aip

// ResourceWildcard is the AIP-159 wildcard, accepted in place of a resource ID
// to mean "across all values of this segment".
//
// See: https://google.aip.dev/159 (Reading across collections).
const ResourceWildcard = "-"

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
