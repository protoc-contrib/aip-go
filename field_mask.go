package aip

import (
	"errors"
	"fmt"

	"github.com/protoc-contrib/aip-go/internal/protopath"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// FieldMaskWildcard is the path meaning "replace the whole resource" — the
// equivalent of PUT rather than PATCH.
//
// See: https://google.aip.dev/134#full-replacement.
const FieldMaskWildcard = "*"

// ErrInvalidFieldMask is returned by [ValidateFieldMask] for a mask that
// cannot be applied to the message. Map it to InvalidArgument at the RPC
// boundary.
var ErrInvalidFieldMask = errors.New("invalid field mask")

// ValidateFieldMask reports whether every path in mask resolves to a field on
// message.
//
// A nil or empty mask is valid and means full replacement, as does the sole
// path "*" — which may not be combined with any other path. Paths are dotted
// and traverse singular message fields only: a mask may *name* a repeated or
// map field, but may not index into one, matching the FieldMask contract that
// paths do not support repeated indices or map keys.
//
// This is a schema check. It says the paths are applicable, not that the
// caller is allowed to write them — see [ValidateRequiredFieldsWithMask] for
// the AIP-134 required-field half.
//
// See: https://google.aip.dev/134#field-masks.
func ValidateFieldMask(mask *fieldmaskpb.FieldMask, message proto.Message) error {
	paths := mask.GetPaths()
	if len(paths) == 0 {
		return nil
	}
	descriptor := message.ProtoReflect().Descriptor()
	for _, path := range paths {
		if path == FieldMaskWildcard {
			if len(paths) != 1 {
				return fmt.Errorf("%w: %q must not be combined with other paths", ErrInvalidFieldMask, FieldMaskWildcard)
			}
			return nil
		}
		if _, err := protopath.Resolve(descriptor, path); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidFieldMask, err)
		}
	}
	return nil
}

// IsFullReplacement reports whether mask asks for the whole resource to be
// replaced, rather than a subset of its fields updated.
//
// That is the case for an absent or empty mask, and for the wildcard "*".
func IsFullReplacement(mask *fieldmaskpb.FieldMask) bool {
	paths := mask.GetPaths()
	return len(paths) == 0 || (len(paths) == 1 && paths[0] == FieldMaskWildcard)
}
