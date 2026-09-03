// Package protopath resolves dotted AIP field paths against protobuf
// messages.
//
// AIP spells nested fields with a `.` separator — `book.author.name` — in
// `order_by` (AIP-132), `update_mask` (AIP-134) and filter comparisons
// (AIP-160). Both path validation and cursor extraction need to walk the
// same grammar, so the walk lives here once.
package protopath

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// Resolve walks path against desc and returns the descriptor of the leaf
// field.
//
// Traversal descends only through singular message fields: a path may not
// continue past a scalar, a list, or a map, because there is no single
// value to descend into. The wildcard path "*" is not accepted here —
// callers that support it must handle it before calling Resolve.
func Resolve(desc protoreflect.MessageDescriptor, path string) (protoreflect.FieldDescriptor, error) {
	if path == "" {
		return nil, errors.New("empty path")
	}
	var leaf protoreflect.FieldDescriptor
	current := desc
	for segment, rest := path, ""; segment != ""; segment, rest = rest, "" {
		if i := strings.IndexByte(segment, '.'); i >= 0 {
			segment, rest = segment[:i], segment[i+1:]
		}
		if current == nil {
			return nil, fmt.Errorf("path %q: field %q is not a message", path, leaf.Name())
		}
		field := current.Fields().ByName(protoreflect.Name(segment))
		if field == nil {
			return nil, fmt.Errorf("path %q: no field %q on %s", path, segment, current.FullName())
		}
		leaf, current = field, nil
		if rest == "" {
			break
		}
		// More segments follow, so this one has to be descendable.
		switch {
		case field.IsList():
			return nil, fmt.Errorf("path %q: cannot traverse into repeated field %q", path, segment)
		case field.IsMap():
			return nil, fmt.Errorf("path %q: cannot traverse into map field %q", path, segment)
		case field.Kind() == protoreflect.MessageKind, field.Kind() == protoreflect.GroupKind:
			current = field.Message()
		default:
			return nil, fmt.Errorf("path %q: cannot traverse into scalar field %q", path, segment)
		}
	}
	return leaf, nil
}

// Leaf is the result of walking a path to its final field.
type Leaf struct {
	// Field describes the leaf field.
	Field protoreflect.FieldDescriptor
	// Value is the leaf field's value, or its zero value when Present is false.
	Value protoreflect.Value
	// Present reports whether the field is set.
	//
	// For fields without explicit presence — bare proto3 scalars — this is
	// always true, because such a field is indistinguishable from one
	// explicitly set to its zero value. It is only meaningful for message
	// fields, `optional` scalars, and oneof members.
	Present bool
}

// Get walks path against msg and returns its leaf.
//
// When an intermediate message along the path is unset, traversal continues
// into its zero value rather than failing: an unset `book.author` yields the
// zero value of `book.author.name`, with Present false. That mirrors
// protobuf's own read semantics and keeps the walk total.
func Get(msg protoreflect.Message, path string) (Leaf, error) {
	if _, err := Resolve(msg.Descriptor(), path); err != nil {
		return Leaf{}, err
	}
	current, present := msg, true
	for segment, rest := path, ""; segment != ""; segment, rest = rest, "" {
		if i := strings.IndexByte(segment, '.'); i >= 0 {
			segment, rest = segment[:i], segment[i+1:]
		}
		fd := current.Descriptor().Fields().ByName(protoreflect.Name(segment))
		// A path is only "present" if every step along it is. Steps without
		// explicit presence never make it absent.
		if fd.HasPresence() && !current.Has(fd) {
			present = false
		}
		value := current.Get(fd)
		if rest == "" {
			return Leaf{Field: fd, Value: value, Present: present}, nil
		}
		current = value.Message()
	}
	return Leaf{}, fmt.Errorf("path %q: unreachable", path)
}
