// Package aip implements the Google API Improvement Proposals in Go.
//
// Everything lives in this one package, so a server reads as
// `aip.ClearFields`, `aip.ValidateFieldMask`, `aip.ResourcePattern` rather
// than a handful of one-type imports. Names are prefixed by the AIP concept
// they belong to, not by a package path.
//
// The AIPs implemented here:
//
//   - AIP-122 resource names — [ResourcePattern], [ResourceName]
//   - AIP-134 field masks — [ValidateFieldMask], [IsFullReplacement]
//   - AIP-203 field behavior — [ClearFields], [ValidateRequiredFields]
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
