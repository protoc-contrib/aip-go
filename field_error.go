package aip

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protopath"
)

// FieldError reports that the value of a request field is invalid: where, and
// why. A parse of a resource name returns one, so a handler need not say which
// field it was parsing. Get the field with errors.As:
//
//	var field *aip.FieldError
//	if errors.As(err, &field) {
//		// field.Field(): "collection.name"
//	}
type FieldError struct {
	// Path is the field, from the message checked: its root, then one step per
	// field the path walks through.
	Path protopath.Path
	// Err is why the value is invalid: for the log, since it can carry the
	// value.
	Err error
}

// NewFieldError returns the [*FieldError] for the field name of message, a
// dotted proto field name as [FieldPath] takes it, that failed with err.
func NewFieldError(message proto.Message, name string, err error) *FieldError {
	return &FieldError{Path: FieldPath(message, name), Err: err}
}

// Field renders the path without its root, as google.rpc.BadRequest names a
// field: `name`, `collection.name`, `requests[1].name`.
func (e *FieldError) Field() string {
	return dottedPath(e.Path)
}

// Error is the field and why, e.g. `collection.name: parse "x": bad segment`.
func (e *FieldError) Error() string {
	if e.Err == nil {
		return e.Field()
	}
	return e.Field() + ": " + e.Err.Error()
}

// Unwrap returns why the value is invalid.
func (e *FieldError) Unwrap() error {
	return e.Err
}

// FieldPath returns the path of the field the dotted proto field name, such as
// `collection.name`, walks to from message, descending into the message each
// field before the last holds. Names are proto field names, not JSON names.
//
// It panics, naming the message and the segment, if the name is not a field of
// message, or walks through a field that holds no message: a name written in a
// handler is checked against the descriptor the first time it runs, not
// shipped to a client as a field that does not exist. message must not be nil;
// a typed-nil one is fine.
func FieldPath(message proto.Message, name string) protopath.Path {
	descriptor := message.ProtoReflect().Descriptor()
	return fieldPath(protopath.Path{protopath.Root(descriptor)}, descriptor, name)
}
