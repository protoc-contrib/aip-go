package aip

import (
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

// isWritable reports whether an update may write field. OUTPUT_ONLY is the
// server's, IDENTIFIER selects the target rather than being part of it, and
// IMMUTABLE was settable once, on create.
func isWritable(field protoreflect.FieldDescriptor) bool {
	return !FieldBehaviorsOf(field).HasAny(OutputOnly, Identifier, Immutable)
}
