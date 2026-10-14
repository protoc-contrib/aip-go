package aip

import (
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// ImpliedFieldMask returns the field mask AIP-134 implies when an update omits
// one: every field of message that an update may write — not OUTPUT_ONLY,
// IDENTIFIER or IMMUTABLE — and that is populated. It reads message and
// returns a new mask; it does not modify anything.
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
//     Clear, then imply: run [ClearFields] with [OutputOnly] first.
//
// The result is never nil. A nil message, or one with no writable field
// populated, yields an empty mask: an update that writes nothing.
//
// A server fills in an omitted mask with it:
//
//	if len(req.GetUpdateMask().GetPaths()) == 0 {
//		aip.ClearFields(req.GetBook(), aip.OutputOnly)
//		req.UpdateMask = aip.ImpliedFieldMask(req.GetBook())
//	}
//
// See: https://google.aip.dev/134#field-masks.
func ImpliedFieldMask(message proto.Message) *fieldmaskpb.FieldMask {
	mask := &fieldmaskpb.FieldMask{}
	if message == nil {
		return mask
	}
	reflected := message.ProtoReflect()
	if !reflected.IsValid() {
		return mask
	}
	fields := reflected.Descriptor().Fields()
	for i := range fields.Len() {
		if field := fields.Get(i); isWritable(field) && reflected.Has(field) {
			mask.Paths = append(mask.Paths, string(field.Name()))
		}
	}
	return mask
}

// ClearFieldMask removes from mask the paths that name, or pass through, a
// field of message annotated kind, keeping the rest in the order mask holds
// them. It modifies mask in place, as [ClearFields] modifies a message, and
// reads message only for its schema.
//
// A path is walked segment by segment, through singular message fields, so
// `carrier.tracking_id` is removed when either `carrier` or `tracking_id` is
// annotated kind. A path that names no field, such as "*", is kept as written:
// whether it applies is the mask's own validation to decide.
//
// A nil mask is left alone. A nil message names no fields, so every path is
// kept.
//
// AIP-203 has a server ignore OUTPUT_ONLY fields an update mask names rather
// than reject the update:
//
//	if len(req.GetUpdateMask().GetPaths()) > 0 {
//		aip.ClearFieldMask(req.GetBook(), aip.OutputOnly, req.GetUpdateMask())
//	}
//
// See: https://google.aip.dev/203#output-only.
func ClearFieldMask(message proto.Message, kind FieldBehaviorKind, mask *fieldmaskpb.FieldMask) {
	if message == nil || mask == nil {
		return
	}
	root := message.ProtoReflect().Descriptor()
	mask.Paths = slices.DeleteFunc(mask.Paths, func(path string) bool {
		// Walk the path from root; a segment that names no field ends it.
		desc := root
		for segment := range strings.SplitSeq(path, ".") {
			if desc == nil {
				return false
			}
			field := desc.Fields().ByName(protoreflect.Name(segment))
			if field == nil {
				return false
			}
			if FieldBehaviorsOf(field).Has(kind) {
				return true
			}
			desc = nil
			if isSingularMessage(field) {
				desc = field.Message()
			}
		}
		return false
	})
}

// isWritable reports whether an update may write field. OUTPUT_ONLY is the
// server's, IDENTIFIER selects the target rather than being part of it, and
// IMMUTABLE was settable once, on create.
func isWritable(field protoreflect.FieldDescriptor) bool {
	return !FieldBehaviorsOf(field).HasAny(OutputOnly, Identifier, Immutable)
}
