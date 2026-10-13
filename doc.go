// Package aip implements the Google API Improvement Proposals in Go.
//
// Everything lives in this one package, so a server reads as
// `aip.ClearFields`, `aip.ValidateFields`, `aip.ResourcePattern` rather than a
// handful of one-type imports. Names are prefixed by the AIP concept they
// belong to, not by a package path.
//
// The AIPs implemented here:
//
//   - AIP-122 resource names — [ResourcePattern], [ResourceName]
//   - AIP-134 field masks — [ImpliedFieldMask] for an update's omitted mask
//   - AIP-203 field behavior — [ClearFields] to unset the fields of a
//     [FieldBehaviorKind], [ValidateFields] to report the ones left unset, as
//     a [*FieldBehaviorError] holding their paths; [FieldBehaviorsOf] reads a
//     field's annotation into a [FieldBehaviorSet], cached per descriptor
//
// The field behavior functions are primitives: they work on any message and
// any kind, and which kinds a service applies, and where, is its own policy.
// AIP-203 defines three uses — clear OUTPUT_ONLY fields on input, clear
// INPUT_ONLY fields on output, and validate REQUIRED fields: every one on a
// create, only the ones the update_mask names on a partial update:
//
//	aip.ClearFields(req, aip.OutputOnly)
//	err := aip.ValidateFields(req.GetBook(), aip.Required, req.GetUpdateMask())
//	aip.ClearFields(resp, aip.InputOnly)
//
// Which field of a request holds the resource, and whether it is a create or
// an update, is the caller's to say: nothing here recognises request shapes.
//
// Validation is split with protovalidate. A request's own fields — `parent`,
// the resource field itself — every value's format, and whether an
// update_mask names real fields stay protovalidate's rules: `required`,
// `min_len`, `field_mask.in`. REQUIRED on a resource's own fields is
// [ValidateFields]': a resource message is shared by its create and update
// requests, and protovalidate cannot tell which method it is validating.
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
