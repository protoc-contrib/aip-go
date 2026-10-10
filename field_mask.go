package aip

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// FieldMaskWildcard is the path meaning "replace the whole resource" — the
// equivalent of PUT rather than PATCH.
//
// See: https://google.aip.dev/134#full-replacement.
const FieldMaskWildcard = "*"

// IsFullReplacement reports whether mask asks for the whole resource to be
// replaced, rather than a subset of its fields updated: exactly the one path
// "*". Expand it with [MutablePaths].
//
// An absent or empty mask is not full replacement. AIP-134 reads it as the
// implied mask — the fields the client populated — which [ImpliedUpdateMask]
// computes. Treating it as full replacement would turn an update that sent
// nothing into one that overwrites every writable field with its zero value.
//
// It is the one AIP-134 question protovalidate cannot answer: whether the paths
// are applicable is its `field_mask.in` rule, but what a shorthand expands to is
// the server's to decide.
//
// See: https://google.aip.dev/134#full-replacement.
func IsFullReplacement(mask *fieldmaskpb.FieldMask) bool {
	paths := mask.GetPaths()
	return len(paths) == 1 && paths[0] == FieldMaskWildcard
}

// MutablePaths returns the fields of desc an update may write — every
// top-level field not OUTPUT_ONLY, IDENTIFIER or IMMUTABLE — in declaration
// order. It is the expansion of the "*" mask; see [IsFullReplacement].
//
//	if aip.IsFullReplacement(x.GetUpdateMask()) {
//		x.UpdateMask = &fieldmaskpb.FieldMask{
//			Paths: aip.MutablePaths(x.GetCollection().ProtoReflect().Descriptor()),
//		}
//	}
//
// A nil desc returns nil.
//
// See: https://google.aip.dev/134#full-replacement.
func MutablePaths(desc protoreflect.MessageDescriptor) []string {
	if desc == nil {
		return nil
	}
	var paths []string
	fields := desc.Fields()
	for i := range fields.Len() {
		if field := fields.Get(i); isWritable(field) {
			paths = append(paths, string(field.Name()))
		}
	}
	return paths
}

// ImpliedUpdateMask returns the update mask AIP-134 implies when a client
// omits one: every field of resource that an update may write — not
// OUTPUT_ONLY, IDENTIFIER or IMMUTABLE — and that is populated. It reads
// resource and returns a new mask; it does not modify anything.
//
// Populated is protobuf presence: an implicit-presence scalar counts when it
// is non-zero, an `optional` one when it is set — even to zero — a message
// when it is set, a repeated field or map when it is non-empty, and a oneof
// member when it is the one set. Only top-level fields are considered, in
// declaration order.
//
// Two consequences of reading presence:
//
//   - An implicit-presence zero value — false, 0, "" — is indistinguishable
//     from unset, so it is never implied. A client clears such a field with
//     an explicit mask, or the schema makes the field `optional`.
//   - A populated message field is implied as one whole path, so the update
//     writes everything beneath it — nested OUTPUT_ONLY values included.
//     Clear, then imply: run [ClearFields] with [OutputOnly] on the resource
//     first.
//
// The result is never nil. A nil resource, or one with no writable field
// populated, yields an empty mask: an update that writes nothing, not full
// replacement — see [IsFullReplacement].
//
// A server fills in an omitted mask with it:
//
//	if len(x.GetUpdateMask().GetPaths()) == 0 {
//		aip.ClearFields(x.GetCollection(), aip.OutputOnly)
//		x.UpdateMask = aip.ImpliedUpdateMask(x.GetCollection())
//	}
//
// See: https://google.aip.dev/134#field-masks.
func ImpliedUpdateMask(resource proto.Message) *fieldmaskpb.FieldMask {
	mask := &fieldmaskpb.FieldMask{}
	if resource == nil {
		return mask
	}
	message := resource.ProtoReflect()
	if !message.IsValid() {
		return mask
	}
	fields := message.Descriptor().Fields()
	for i := range fields.Len() {
		if field := fields.Get(i); isWritable(field) && message.Has(field) {
			mask.Paths = append(mask.Paths, string(field.Name()))
		}
	}
	return mask
}

// isWritable reports whether an update may write field: the one place the rule
// lives, for both [MutablePaths] and [ImpliedUpdateMask]. OUTPUT_ONLY is the
// server's, IDENTIFIER selects the target rather than being part of it, and
// IMMUTABLE was settable once, on create.
func isWritable(field protoreflect.FieldDescriptor) bool {
	return !HasFieldBehavior(field, OutputOnly) &&
		!HasFieldBehavior(field, Identifier) &&
		!HasFieldBehavior(field, Immutable)
}
