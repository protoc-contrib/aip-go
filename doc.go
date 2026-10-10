// Package aip implements the Google API Improvement Proposals in Go.
//
// Everything lives in this one package, so a server reads as
// `aip.ClearFields`, `aip.IsFullReplacement`, `aip.ResourcePattern` rather
// than a handful of one-type imports. Names are prefixed by the AIP concept
// they belong to, not by a package path.
//
// The AIPs implemented here:
//
//   - AIP-122 resource names — [ResourcePattern], [ResourceName]
//   - AIP-134 field masks — [IsFullReplacement] and [MutablePaths] for "*",
//     [ImpliedUpdateMask] for an omitted mask
//   - AIP-203 field behavior — [ClearFields], [CopyFields], and
//     [ImmutableChanges] to refuse a changed IMMUTABLE value
//
// This stack is deliberately stricter than AIP-161 about update masks. AIP-161
// has a mask that names an OUTPUT_ONLY field ignored; here protovalidate's
// `field_mask.in` lists only the writable fields, so such a mask is rejected
// with InvalidArgument — a clearer answer than a silent no-op, and one that
// protoc-gen-aip-lint's update-mask-writable-fields rule keeps in step with
// `google.api.field_behavior`. A changed IMMUTABLE value is an error too
// ([ImmutableChanges]), rather than dropped by the implied mask or by "*".
//
// Validation is deliberately absent too. Whether a REQUIRED field is set and
// whether an update_mask names real fields are protovalidate's rules —
// `required` or `min_len`, and `field_mask.in` — which a server already runs;
// a second implementation here could only disagree with it.
//
// Ordering (AIP-132), pagination (AIP-158) and filtering (AIP-160) are
// deliberately absent. A page token or cursor is only meaningful against the
// ordering, column map and SQL the query layer actually runs, so it belongs
// there, as in the Rust stack, where aip-rs has none either. Filters are
// plain CEL: protoc-gen-go-aip compiles them to a *cel.Ast for the query
// layer to transpile.
//
// See: https://google.aip.dev.
package aip
