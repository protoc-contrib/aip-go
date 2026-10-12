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
//   - AIP-203 field behavior — [ClearFields], [CopyFields],
//     [MissingRequiredPaths] to enforce REQUIRED on a create or an update, and
//     [ChangedImmutablePaths] to refuse a changed IMMUTABLE value
//
// This stack is deliberately stricter than AIP-161 about update masks. AIP-161
// has a mask that names an OUTPUT_ONLY field ignored; here protovalidate's
// `field_mask.in` lists only the writable fields, so such a mask is rejected
// with InvalidArgument — a clearer answer than a silent no-op, and one that
// protoc-gen-aip-lint's update-mask-writable-fields rule keeps in step with
// `google.api.field_behavior`. A changed IMMUTABLE value is an error too
// ([ChangedImmutablePaths]), rather than dropped by the implied mask or by "*".
//
// Validation is split with protovalidate. A request's own fields — `parent`,
// the resource field itself — every value's format, and whether an
// update_mask names real fields stay protovalidate's rules: `required`,
// `min_len`, `field_mask.in`. REQUIRED on a resource's own fields is
// [MissingRequiredPaths]'s: a resource message is shared by its create and
// update requests, a create must set every REQUIRED field while an update need
// only set the ones its mask names, and protovalidate cannot tell which method
// it is validating. Per-request CEL rules were the workaround this replaces.
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
