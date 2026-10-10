package aip

import (
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
