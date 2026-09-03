package aip

import (
	"errors"
	"fmt"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// FieldBehavior is the documented behavior of a field, as declared by the
// `google.api.field_behavior` annotation.
//
// See: https://google.aip.dev/203 (Field behavior documentation).
type FieldBehavior = annotations.FieldBehavior

// The field behaviors, re-exported so that callers need not import
// google.golang.org/genproto/googleapis/api/annotations directly.
const (
	// Optional marks a field the client may omit.
	Optional = annotations.FieldBehavior_OPTIONAL
	// Required marks a field the client must populate.
	Required = annotations.FieldBehavior_REQUIRED
	// OutputOnly marks a field set by the server and ignored on input.
	OutputOnly = annotations.FieldBehavior_OUTPUT_ONLY
	// InputOnly marks a field accepted on input but never returned.
	InputOnly = annotations.FieldBehavior_INPUT_ONLY
	// Immutable marks a field settable on create but not on update.
	Immutable = annotations.FieldBehavior_IMMUTABLE
	// UnorderedList marks a repeated field whose order is not significant.
	UnorderedList = annotations.FieldBehavior_UNORDERED_LIST
	// NonEmptyDefault marks a field whose default is a non-empty value.
	NonEmptyDefault = annotations.FieldBehavior_NON_EMPTY_DEFAULT
	// Identifier marks the field holding the resource name.
	Identifier = annotations.FieldBehavior_IDENTIFIER
)

// ErrMissingRequiredField is returned by [ValidateRequiredFields] when a field
// annotated REQUIRED has no value. Map it to InvalidArgument at the RPC
// boundary.
var ErrMissingRequiredField = errors.New("missing required field")

// FieldBehaviors returns the behaviors annotated on field.
func FieldBehaviors(field protoreflect.FieldDescriptor) []FieldBehavior {
	behaviors, ok := proto.GetExtension(field.Options(), annotations.E_FieldBehavior).([]FieldBehavior)
	if !ok {
		return nil
	}
	return behaviors
}

// HasFieldBehavior reports whether field is annotated with want.
func HasFieldBehavior(field protoreflect.FieldDescriptor, want FieldBehavior) bool {
	for _, behavior := range FieldBehaviors(field) {
		if behavior == want {
			return true
		}
	}
	return false
}

// ClearFields recursively clears every field annotated with any of behaviors.
//
// Call it on an inbound request with [OutputOnly] to drop server-owned values
// a client should not be able to set, rather than rejecting the request:
//
//	aip.ClearFields(request.GetShipment(), aip.OutputOnly)
//
// See: https://google.aip.dev/161#output-only-fields.
func ClearFields(message proto.Message, behaviors ...FieldBehavior) {
	if len(behaviors) == 0 {
		return
	}
	clearFields(message.ProtoReflect(), behaviors)
}

func clearFields(message protoreflect.Message, behaviors []FieldBehavior) {
	// Collect before clearing: mutating during Range is not permitted.
	var clear []protoreflect.FieldDescriptor
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if hasAnyFieldBehavior(field, behaviors) {
			clear = append(clear, field)
			// No point descending into a subtree that is about to be removed.
			return true
		}
		rangeMessages(field, value, func(nested protoreflect.Message) {
			clearFields(nested, behaviors)
		})
		return true
	})
	for _, field := range clear {
		message.Clear(field)
	}
}

// CopyFields copies every field annotated with any of behaviors from src to
// dst, clearing those that are unset on src.
//
// Use it to restore server-owned values onto a message a client sent, after
// [ClearFields] has removed whatever the client tried to set.
func CopyFields(dst, src proto.Message, behaviors ...FieldBehavior) error {
	dstReflect, srcReflect := dst.ProtoReflect(), src.ProtoReflect()
	if dstReflect.Descriptor() != srcReflect.Descriptor() {
		return fmt.Errorf(
			"copy fields: dst is %s but src is %s",
			dstReflect.Descriptor().FullName(), srcReflect.Descriptor().FullName(),
		)
	}
	if len(behaviors) == 0 {
		return nil
	}
	fields := dstReflect.Descriptor().Fields()
	for i := range fields.Len() {
		field := fields.Get(i)
		if !hasAnyFieldBehavior(field, behaviors) {
			continue
		}
		if isFieldPopulated(srcReflect, field) {
			dstReflect.Set(field, srcReflect.Get(field))
		} else {
			dstReflect.Clear(field)
		}
	}
	return nil
}

// ValidateRequiredFields returns [ErrMissingRequiredField] if any field
// annotated REQUIRED — at any depth — has no value.
//
// A field without explicit presence cannot distinguish "unset" from "set to
// the zero value", so a REQUIRED proto3 scalar must be non-zero to count as
// present: a required `bool` can never be false. Declare it `optional` if
// false is a legitimate value.
//
// See: https://google.aip.dev/203.
func ValidateRequiredFields(message proto.Message) error {
	return validateRequiredFields(message.ProtoReflect(), nil, "")
}

// ValidateRequiredFieldsWithMask is [ValidateRequiredFields] restricted to the
// fields named by mask, for validating a partial update (AIP-134).
//
// Matching is by prefix, so a mask of ["shipment"] validates the required
// fields nested beneath `shipment` as well as `shipment` itself — replacing a
// subtree wholesale means the whole subtree has to be valid. A nil or empty
// mask validates nothing, since a request that names no paths updates nothing.
func ValidateRequiredFieldsWithMask(message proto.Message, mask *fieldmaskpb.FieldMask) error {
	if len(mask.GetPaths()) == 0 {
		return nil
	}
	return validateRequiredFields(message.ProtoReflect(), mask, "")
}

func validateRequiredFields(message protoreflect.Message, mask *fieldmaskpb.FieldMask, prefix string) error {
	fields := message.Descriptor().Fields()
	for i := range fields.Len() {
		field := fields.Get(i)
		path := string(field.Name())
		if prefix != "" {
			path = prefix + "." + path
		}
		if !isFieldPopulated(message, field) {
			if HasFieldBehavior(field, Required) && maskCovers(mask, path) {
				return fmt.Errorf("%w: %s", ErrMissingRequiredField, path)
			}
			continue
		}
		var err error
		rangeMessages(field, message.Get(field), func(nested protoreflect.Message) {
			if err == nil {
				err = validateRequiredFields(nested, mask, path)
			}
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// maskCovers reports whether path is named by mask, either exactly or as a
// descendant of one of its paths. A nil mask covers everything.
func maskCovers(mask *fieldmaskpb.FieldMask, path string) bool {
	if len(mask.GetPaths()) == 0 {
		return true
	}
	for _, masked := range mask.GetPaths() {
		if masked == FieldMaskWildcard || masked == path {
			return true
		}
		if len(masked) < len(path) && path[:len(masked)] == masked && path[len(masked)] == '.' {
			return true
		}
	}
	return false
}

// rangeMessages invokes fn for every message reachable through field, whether
// it is a singular message, a repeated message, or a map with message values.
// Non-message fields yield nothing.
func rangeMessages(
	field protoreflect.FieldDescriptor,
	value protoreflect.Value,
	fn func(protoreflect.Message),
) {
	switch {
	case field.IsMap():
		if field.MapValue().Kind() != protoreflect.MessageKind {
			return
		}
		value.Map().Range(func(_ protoreflect.MapKey, entry protoreflect.Value) bool {
			fn(entry.Message())
			return true
		})
	case field.IsList():
		if field.Kind() != protoreflect.MessageKind && field.Kind() != protoreflect.GroupKind {
			return
		}
		list := value.List()
		for i := range list.Len() {
			fn(list.Get(i).Message())
		}
	case field.Kind() == protoreflect.MessageKind, field.Kind() == protoreflect.GroupKind:
		fn(value.Message())
	}
}

func hasAnyFieldBehavior(field protoreflect.FieldDescriptor, behaviors []FieldBehavior) bool {
	for _, behavior := range behaviors {
		if HasFieldBehavior(field, behavior) {
			return true
		}
	}
	return false
}

// isFieldPopulated reports whether field carries a value on message.
//
// Fields with explicit presence — message fields, `optional` scalars, oneof
// members — are answered by presence, so an explicit false or 0 counts as
// populated. Everything else has no presence bit on the wire, and the only
// available reading of "unset" is "zero".
func isFieldPopulated(message protoreflect.Message, field protoreflect.FieldDescriptor) bool {
	if field.HasPresence() {
		return message.Has(field)
	}
	value := message.Get(field)
	switch {
	case !value.IsValid():
		return false
	case field.IsList():
		return value.List().Len() > 0
	case field.IsMap():
		return value.Map().Len() > 0
	}
	switch field.Kind() {
	case protoreflect.BoolKind:
		return value.Bool()
	case protoreflect.EnumKind:
		return value.Enum() != 0
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return value.Int() != 0
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return value.Uint() != 0
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return value.Float() != 0
	case protoreflect.StringKind:
		return value.String() != ""
	case protoreflect.BytesKind:
		return len(value.Bytes()) > 0
	default:
		return value.IsValid()
	}
}
