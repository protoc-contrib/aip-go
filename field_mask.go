package aip

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// FieldMaskWildcard is the path meaning "replace the whole resource" — the
// equivalent of PUT rather than PATCH.
//
// See: https://google.aip.dev/134#full-replacement.
const FieldMaskWildcard = "*"

// IsFullReplacement reports whether mask asks for the whole resource to be
// replaced, rather than a subset of its fields updated.
//
// That is the case for an absent or empty mask, and for the wildcard "*". It
// is the one AIP-134 question protovalidate cannot answer: whether the paths
// are applicable is its `field_mask.in` rule, but what an empty mask expands
// to is the server's to decide.
func IsFullReplacement(mask *fieldmaskpb.FieldMask) bool {
	paths := mask.GetPaths()
	return len(paths) == 0 || (len(paths) == 1 && paths[0] == FieldMaskWildcard)
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
// The result is never nil. A nil resource, or one with no writable field
// populated, yields an empty mask — which, like an absent one, reads as full
// replacement to [IsFullReplacement].
//
// A server fills in an omitted mask with it:
//
//	if len(x.GetUpdateMask().GetPaths()) == 0 {
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
		field := fields.Get(i)
		if HasFieldBehavior(field, OutputOnly) ||
			HasFieldBehavior(field, Identifier) ||
			HasFieldBehavior(field, Immutable) {
			continue
		}
		if message.Has(field) {
			mask.Paths = append(mask.Paths, string(field.Name()))
		}
	}
	return mask
}
