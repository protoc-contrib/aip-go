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
//   - AIP-133 create — [ImplyResourceName] to assign an omitted id and build
//     the resource's name on a whole request
//   - AIP-134 field masks — [IsFullReplacement] and [MutablePaths] for "*",
//     [ImpliedUpdateMask] for an omitted mask, and [ImplyUpdateMask] to fill
//     one in on a whole request; [ImplyRequest] runs both implications
//   - AIP-203 field behavior — [ClearFields], [CopyFields],
//     [ValidateRequiredFields] to enforce REQUIRED on a create or an update,
//     [ValidateRequired] to do so on a whole request, and
//     [ValidateImmutable] to refuse a changed IMMUTABLE value; they return a
//     [*RequiredError] or [*ImmutableError] holding the fields' paths
//
// The request-level functions recognise the AIP standard method requests by
// their shape: a create (AIP-133) carries its resource in the field whose type
// declares google.api.resource, an update (AIP-134) also has an update_mask,
// and a batch (AIP-233, AIP-235) carries such requests in `requests`. A server
// runs [ClearFields] with [OutputOnly], then [ImplyRequest], then
// [ValidateRequired], on every request.
//
// This stack is deliberately stricter than AIP-161 about update masks. AIP-161
// has a mask that names an OUTPUT_ONLY field ignored; here protovalidate's
// `field_mask.in` lists only the writable fields, so such a mask is rejected
// with InvalidArgument — a clearer answer than a silent no-op, and one that
// protoc-gen-aip-lint's update-mask-writable-fields rule keeps in step with
// `google.api.field_behavior`. A changed IMMUTABLE value is an error too
// ([ValidateImmutable]), rather than dropped by the implied mask or by "*".
//
// Validation is split with protovalidate. A request's own fields — `parent`,
// the resource field itself — every value's format, and whether an
// update_mask names real fields stay protovalidate's rules: `required`,
// `min_len`, `field_mask.in`. REQUIRED on a resource's own fields is
// [ValidateRequiredFields]'s: a resource message is shared by its create and
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
