// Package protopath resolves dotted AIP field paths against protobuf
// messages.
//
// AIP spells nested fields with a `.` separator — `book.author.name` — in
// `update_mask` (AIP-134) paths, which are validated by walking that
// grammar here.
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
