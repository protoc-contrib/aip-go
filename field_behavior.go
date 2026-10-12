package aip

import (
	"fmt"
	"strings"

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

// MissingRequiredPaths returns the paths of the fields of resource annotated
// REQUIRED that are unset, in declaration order, depth first, with a nested
// field's path dot-joined to its parent's (`carrier.name`). It reads resource
// and modifies nothing.
//
// AIP-203 asks a different thing of a create and of a partial update, and the
// mask says which one this is:
//
//   - A nil mask is a create, which must set every REQUIRED field. A nested
//     message's annotations are independent of its parent's and apply only
//     when the parent is set, so a set message field is checked in turn and an
//     unset one is not.
//   - A non-nil mask is a partial update, which may omit a REQUIRED field the
//     mask does not name; one it names must be set, or the update would clear
//     it. A path naming a message field replaces the whole message, so the
//     field and, when it is set, everything beneath it are checked as on
//     create; a nested path such as `carrier.name` checks just that field.
//     An empty mask names nothing and reports nothing.
//
// An update that omitted its mask must have the implied mask filled in first,
// with [ImpliedUpdateMask]: `GetUpdateMask()` on such a request is nil, which
// reads as a create and checks every field.
//
// The mask's paths are matched as written: "*" names no field here, so
// expand it with [MutablePaths] first, and paths that name no field are
// ignored — whether they are valid is protovalidate's `field_mask.in`. A
// repeated or map field is checked itself but never descended into, since a
// mask path cannot address its elements.
//
// Unset is protobuf presence, as [ImpliedUpdateMask] reads it: a message,
// `optional` scalar or oneof member is judged present or absent, so an
// explicit `false` satisfies a REQUIRED `optional bool`, while a presence-less
// scalar is unset at its zero value — a REQUIRED `bool` must be true, as
// AIP-203 says.
//
// protovalidate keeps a request's own fields — `parent`, the resource field
// itself — and every value's format; this is for the resource's fields. A
// resource message is shared by its create and update requests, and
// protovalidate cannot tell which method it is validating, so REQUIRED on a
// resource field is not something its rules can say:
//
//	if missing := aip.MissingRequiredPaths(req.GetCollection(), req.GetUpdateMask()); len(missing) > 0 {
//		return status.Errorf(codes.InvalidArgument, "required fields are missing: %v", missing)
//	}
//
// A nil or typed-nil resource yields nil.
//
// See: https://google.aip.dev/203#required.
func MissingRequiredPaths(resource proto.Message, mask *fieldmaskpb.FieldMask) []string {
	if resource == nil {
		return nil
	}
	message := resource.ProtoReflect()
	if !message.IsValid() {
		return nil
	}
	return missingRequiredPaths(message, "", mask != nil, mask.GetPaths(), nil)
}

// missingRequiredPaths appends to missing the REQUIRED fields of message that
// are unset, each prefixed with prefix. Unless masked, every field is checked;
// otherwise only those paths names, relative to message.
func missingRequiredPaths(
	message protoreflect.Message,
	prefix string,
	masked bool,
	paths []string,
	missing []string,
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
		if whole && HasFieldBehavior(field, Required) && !isFieldPopulated(message, field) {
			missing = append(missing, prefix+name)
		}
		if !isSingularMessage(field) {
			continue
		}
		switch {
		case whole && message.Has(field):
			// Replaced wholesale, or created: check it as a create would.
			missing = missingRequiredPaths(message.Get(field).Message(), prefix+name+".", false, nil, missing)
		case !whole:
			// Only nested paths are named, and they are written whether or not
			// the request sets the message, so an unset one reads as empty.
			missing = missingRequiredPaths(message.Get(field).Message(), prefix+name+".", true, nested, missing)
		}
	}
	return missing
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

// ChangedImmutablePaths returns the top-level fields annotated IMMUTABLE that update
// populates with a value different from the one existing holds, in declaration
// order. It reads both messages and modifies neither.
//
// AIP-203: an IMMUTABLE field may be set on create and never changed
// afterwards, and a service must reject a request that tries. A client echoing
// the stored value back is not changing it, and a field update leaves unset is
// not part of the request — neither is reported. Neither the implied update
// mask nor the expansion of "*" includes an IMMUTABLE field, so without this
// check a changed value would be dropped silently rather than refused:
//
//	stored, err := repository.Get(ctx, name)
//	// ...
//	if changed := aip.ChangedImmutablePaths(stored, req.GetCollection()); len(changed) > 0 {
//		return status.Errorf(codes.InvalidArgument, "immutable fields cannot change: %v", changed)
//	}
//
// Values are compared as [proto.Equal] compares them, so a message, repeated
// or map field is compared by content. A repeated field also annotated
// UNORDERED_LIST is compared as a multiset — the same elements, each as often,
// in any order — since AIP-203 lets the service reorder such a list, and a
// client echoing it back in the order it was returned is not changing it. A
// nil existing or update — or a typed
// nil — yields nil. existing and update must be the same message type;
// different types panic, as [proto.Merge] does, since that is a programming
// error rather than something a request can cause.
//
// See: https://google.aip.dev/203#immutable.
func ChangedImmutablePaths(existing, update proto.Message) []string {
	if existing == nil || update == nil {
		return nil
	}
	existingReflect, updateReflect := existing.ProtoReflect(), update.ProtoReflect()
	if !existingReflect.IsValid() || !updateReflect.IsValid() {
		return nil
	}
	if existingReflect.Descriptor() != updateReflect.Descriptor() {
		panic(fmt.Sprintf(
			"changed immutable paths: existing is %s but update is %s",
			existingReflect.Descriptor().FullName(), updateReflect.Descriptor().FullName(),
		))
	}
	var changed []string
	fields := updateReflect.Descriptor().Fields()
	for i := range fields.Len() {
		field := fields.Get(i)
		if !HasFieldBehavior(field, Immutable) || !updateReflect.Has(field) {
			continue
		}
		if !fieldEqual(existingReflect, updateReflect, field) {
			changed = append(changed, string(field.Name()))
		}
	}
	return changed
}

// fieldEqual reports whether a and b hold the same value for field, compared
// as [proto.Equal] compares it: each value is copied alone into a fresh message
// of the type, so scalars, messages, lists and maps all get proto.Equal's
// semantics without restating them here. An UNORDERED_LIST field is compared
// as a multiset instead.
func fieldEqual(a, b protoreflect.Message, field protoreflect.FieldDescriptor) bool {
	if field.IsList() && HasFieldBehavior(field, UnorderedList) {
		return unorderedEqual(a.Get(field).List(), b.Get(field).List())
	}
	only := func(m protoreflect.Message) proto.Message {
		single := m.New()
		if m.Has(field) {
			single.Set(field, m.Get(field))
		}
		return single.Interface()
	}
	return proto.Equal(only(a), only(b))
}

// unorderedEqual reports whether a and b hold the same elements, each as
// often, in any order, comparing elements as [proto.Equal] does.
func unorderedEqual(a, b protoreflect.List) bool {
	if a.Len() != b.Len() {
		return false
	}
	matched := make([]bool, b.Len())
	for i := range a.Len() {
		found := false
		for j := range b.Len() {
			if !matched[j] && a.Get(i).Equal(b.Get(j)) {
				matched[j], found = true, true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
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
