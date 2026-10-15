package aip

import (
	"fmt"
	"iter"
	"math/bits"
	"strings"
	"sync"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protopath"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// FieldBehaviorKind is a documented behavior of a field, as declared by the
// `google.api.field_behavior` annotation.
//
// See: https://google.aip.dev/203 (Field behavior documentation).
type FieldBehaviorKind = annotations.FieldBehavior

// The field behavior kinds, re-exported so that callers need not import
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

// FieldBehaviorSet is a set of [FieldBehaviorKind] values: the behaviors a
// field is annotated with, as [FieldBehaviorsOf] reads them. The zero value is
// the empty set. Each kind is one bit, so a set is cheap to copy and compare.
type FieldBehaviorSet uint16

// FieldBehaviorsOf returns the behaviors field is annotated with in
// `google.api.field_behavior`.
//
// The annotation is decoded once per descriptor and cached, so repeated calls
// — one per field on every request — cost a map lookup. It is safe for
// concurrent use.
func FieldBehaviorsOf(field protoreflect.FieldDescriptor) FieldBehaviorSet {
	if cached, ok := fieldBehaviorSets.Load(field); ok {
		return cached.(FieldBehaviorSet)
	}
	var set FieldBehaviorSet
	kinds, _ := proto.GetExtension(field.Options(), annotations.E_FieldBehavior).([]FieldBehaviorKind)
	for _, kind := range kinds {
		set |= fieldBehaviorBit(kind)
	}
	fieldBehaviorSets.Store(field, set)
	return set
}

// fieldBehaviorSets caches [FieldBehaviorsOf] by field descriptor.
var fieldBehaviorSets sync.Map

// fieldBehaviorBit is kind's bit in a [FieldBehaviorSet], or none for a value
// beyond the set's width.
func fieldBehaviorBit(kind FieldBehaviorKind) FieldBehaviorSet {
	if kind < 0 || kind >= fieldBehaviorSetWidth {
		return 0
	}
	return 1 << kind
}

// fieldBehaviorSetWidth is the number of kinds a [FieldBehaviorSet] can hold.
const fieldBehaviorSetWidth = 16

// Has reports whether the set contains kind.
func (s FieldBehaviorSet) Has(kind FieldBehaviorKind) bool {
	bit := fieldBehaviorBit(kind)
	return bit != 0 && s&bit != 0
}

// HasAny reports whether the set contains at least one of kinds.
func (s FieldBehaviorSet) HasAny(kinds ...FieldBehaviorKind) bool {
	for _, kind := range kinds {
		if s.Has(kind) {
			return true
		}
	}
	return false
}

// All yields the kinds in the set, in enum order.
func (s FieldBehaviorSet) All() iter.Seq[FieldBehaviorKind] {
	return func(yield func(FieldBehaviorKind) bool) {
		for kind := FieldBehaviorKind(0); kind < fieldBehaviorSetWidth; kind++ {
			if s.Has(kind) && !yield(kind) {
				return
			}
		}
	}
}

// String lists the kinds in enum order, e.g. "[REQUIRED IMMUTABLE]".
func (s FieldBehaviorSet) String() string {
	names := make([]string, 0, bits.OnesCount16(uint16(s)))
	for kind := range s.All() {
		names = append(names, kind.String())
	}
	return "[" + strings.Join(names, " ") + "]"
}

// ClearFields unsets every field of message annotated kind, at any depth:
// through singular message fields, oneof members, and the elements of
// repeated and map message fields. A field it clears is not descended into.
// A nil or typed-nil message is left alone.
//
// It works on any message and any kind; which kinds a service clears, and
// where, is its own policy. AIP-203 has a server clear OUTPUT_ONLY fields on
// input, rather than reject them, and never return INPUT_ONLY ones:
//
//	aip.ClearFields(request, aip.OutputOnly)
//	aip.ClearFields(response, aip.InputOnly)
//
// See: https://google.aip.dev/203#output-only.
func ClearFields(message proto.Message, kind FieldBehaviorKind) {
	if message == nil {
		return
	}
	reflected := message.ProtoReflect()
	if !reflected.IsValid() {
		return
	}
	clearFields(reflected, kind)
}

func clearFields(message protoreflect.Message, kind FieldBehaviorKind) {
	// Collect before clearing: mutating during Range is not permitted.
	var clear []protoreflect.FieldDescriptor
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if FieldBehaviorsOf(field).Has(kind) {
			clear = append(clear, field)
			// No point descending into a subtree that is about to be removed.
			return true
		}
		rangeMessages(field, value, func(nested protoreflect.Message) {
			clearFields(nested, kind)
		})
		return true
	})
	for _, field := range clear {
		message.Clear(field)
	}
}

// ValidateFields returns a [*FieldBehaviorError] naming the fields of message
// annotated kind that are unset, as paths from message, in declaration order,
// depth first; nil when there are none. It reads message and modifies nothing.
//
// The mask says which fields are checked:
//
//   - A nil mask checks every field, as a create must set every REQUIRED one.
//     A nested message's annotations are independent of its parent's and apply
//     only when the parent is set, so a set message field is checked in turn
//     and an unset one is not.
//   - A non-nil mask checks only the fields its paths name, as a partial update
//     may omit a REQUIRED field its mask does not name but must set one it
//     names, or the update would clear it. A path naming a message field covers
//     the whole message: the field and, when it is set, everything beneath it
//     are checked as with a nil mask. A nested path such as `carrier.name`
//     checks just that field, as unset when `carrier` is: the update writes it
//     either way. An empty mask names nothing and reports nothing.
//
// The mask's paths are matched as written: "*" names no field, and a path that
// names no field is ignored. A repeated or map field is checked itself but
// never descended into, since a mask path cannot address its elements.
//
// Unset is protobuf presence: a message, `optional` scalar or oneof member is
// judged present or absent, so an explicit `false` satisfies a REQUIRED
// `optional bool`, while a presence-less scalar is unset at its zero value — a
// REQUIRED `bool` must be true, as AIP-203 says.
//
// It works on any message and any kind; AIP-203 defines it for REQUIRED. On an
// update, fill in an omitted mask with [ImpliedFieldMask] first: a nil mask
// checks every field.
//
//	if err := aip.ValidateFields(req.GetBook(), aip.Required, req.GetUpdateMask()); err != nil {
//		return status.Error(codes.InvalidArgument, err.Error())
//	}
//
// A nil or typed-nil message is valid.
//
// See: https://google.aip.dev/203#required.
func ValidateFields(message proto.Message, kind FieldBehaviorKind, mask *fieldmaskpb.FieldMask) error {
	if message == nil {
		return nil
	}
	reflected := message.ProtoReflect()
	if !reflected.IsValid() {
		return nil
	}
	root := protopath.Path{protopath.Root(reflected.Descriptor())}
	var paths []protopath.Path
	for _, name := range unsetPaths(reflected, kind, "", mask != nil, mask.GetPaths(), nil) {
		paths = append(paths, fieldPath(root, reflected.Descriptor(), name))
	}
	if len(paths) == 0 {
		return nil
	}
	return &FieldBehaviorError{Kind: kind, Paths: paths}
}

// FieldBehaviorError reports the fields of one kind that failed a check, as
// [ValidateFields] returns it. Get the paths with errors.As:
//
//	var fields *aip.FieldBehaviorError
//	if errors.As(err, &fields) {
//		for _, path := range fields.Paths {
//			// path.String(): "(acme.v1.Book).author.name"
//		}
//	}
type FieldBehaviorError struct {
	// Kind is the behavior the fields are annotated with.
	Kind FieldBehaviorKind
	// Paths are the fields that failed, each from the message checked, in the
	// order it holds them.
	Paths []protopath.Path
}

// Error lists the fields, e.g.
// "REQUIRED fields are unset: title, author.name".
func (e *FieldBehaviorError) Error() string {
	return e.Kind.String() + " fields are unset: " + joinPaths(e.Paths)
}

// unsetPaths appends to unset the fields of message annotated kind that are
// unset, each prefixed with prefix. Unless masked, every field is checked;
// otherwise only those paths names, relative to message.
func unsetPaths(
	message protoreflect.Message,
	kind FieldBehaviorKind,
	prefix string,
	masked bool,
	paths []string,
	unset []string,
) []string {
	fields := message.Descriptor().Fields()
	for i := range fields.Len() {
		field := fields.Get(i)
		name := string(field.Name())
		whole, nested := !masked, []string(nil)
		if masked {
			whole, nested = maskedPaths(paths, name)
		}
		if !whole && len(nested) == 0 {
			continue
		}
		if whole && FieldBehaviorsOf(field).Has(kind) && !isFieldPopulated(message, field) {
			unset = append(unset, prefix+name)
		}
		if !isSingularMessage(field) {
			continue
		}
		switch {
		case whole && message.Has(field):
			// Covered whole: check it as with a nil mask.
			unset = unsetPaths(message.Get(field).Message(), kind, prefix+name+".", false, nil, unset)
		case !whole:
			// Only nested paths are named, and they are written whether or not
			// the message is set, so an unset one reads as empty.
			unset = unsetPaths(message.Get(field).Message(), kind, prefix+name+".", true, nested, unset)
		}
	}
	return unset
}

// maskedPaths reports whether paths names the field name itself, and returns
// the paths beneath it, relative to it. A path is matched segment by segment,
// so `orig` names neither `origin` nor anything beneath it.
func maskedPaths(paths []string, name string) (whole bool, nested []string) {
	for _, path := range paths {
		if path == name {
			whole = true
		} else if rest, ok := strings.CutPrefix(path, name+"."); ok {
			nested = append(nested, rest)
		}
	}
	return whole, nested
}

// isSingularMessage reports whether field holds one message: not repeated, not
// a map, and of message or group kind.
func isSingularMessage(field protoreflect.FieldDescriptor) bool {
	return !field.IsList() && !field.IsMap() &&
		(field.Kind() == protoreflect.MessageKind || field.Kind() == protoreflect.GroupKind)
}

// fieldPath returns at extended with the fields the dotted name walks through,
// starting from message. It panics, naming the message and the segment, if one
// is no field of the message it is looked up in, or if a message is expected
// of a field that holds none.
func fieldPath(at protopath.Path, message protoreflect.MessageDescriptor, name string) protopath.Path {
	path := append(protopath.Path(nil), at...)
	for segment := range strings.SplitSeq(name, ".") {
		if message == nil {
			panic(fmt.Sprintf("aip.FieldPath: %q is not a message, so %q is no field of it", dottedPath(path), segment))
		}
		field := message.Fields().ByName(protoreflect.Name(segment))
		if field == nil {
			panic(fmt.Sprintf("aip.FieldPath: %s has no field %q", message.FullName(), segment))
		}
		path = append(path, protopath.FieldAccess(field))
		message = field.Message()
	}
	return path
}

// joinPaths renders paths as a comma-separated list of dotted paths, each
// without its root step: `title`, `author.name`.
func joinPaths(paths []protopath.Path) string {
	rendered := make([]string, len(paths))
	for i, path := range paths {
		rendered[i] = dottedPath(path)
	}
	return strings.Join(rendered, ", ")
}

// dottedPath renders path as field names joined by dots, list indices in
// brackets, without its root step: `stops[1].city`.
func dottedPath(path protopath.Path) string {
	var b strings.Builder
	for _, step := range path {
		switch step.Kind() {
		case protopath.FieldAccessStep:
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			b.WriteString(string(step.FieldDescriptor().Name()))
		case protopath.ListIndexStep:
			fmt.Fprintf(&b, "[%d]", step.ListIndex())
		}
	}
	return b.String()
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
