// Package aip implements the Google API Improvement Proposals in Go.
//
// Everything lives in this one package, so a server reads as `aip.OrderBy`,
// `aip.PageToken`, `aip.Filter` rather than a handful of one-type imports.
// Names are prefixed by the AIP concept they belong to, not by a package
// path: [ParseOrderBy], [ParsePageToken], [ParseFilter].
//
// The pieces compose into one List handler:
//
//	orderBy, err := aip.ParseOrderBy(request)
//	if err != nil {
//	        return nil, connect.NewError(connect.CodeInvalidArgument, err)
//	}
//	if err := orderBy.ValidateForPaths("title", "create_time"); err != nil {
//	        return nil, connect.NewError(connect.CodeInvalidArgument, err)
//	}
//	token, err := aip.ParsePageToken(request)
//	if err != nil {
//	        return nil, connect.NewError(connect.CodeInvalidArgument, err)
//	}
//
//	books := query(token, orderBy, request.GetPageSize())
//
//	var nextPageToken string
//	if len(books) == int(request.GetPageSize()) {
//	        token, err = token.NextCursor(books[len(books)-1], orderBy.Paths()...)
//	        if err != nil {
//	                return nil, err
//	        }
//	        if nextPageToken, err = token.Encode(); err != nil {
//	                return nil, err
//	        }
//	}
//
// The AIPs implemented here:
//
//   - AIP-122 resource names — [ResourcePattern] (the runtime behind generated name types)
//   - AIP-132 ordering — [OrderBy], [ParseOrderBy]
//   - AIP-134 field masks — [ValidateFieldMask], [IsFullReplacement]
//   - AIP-158 pagination — [PageToken], [ParsePageToken], [Cursor]
//   - AIP-203 field behavior — [ClearFields], [ValidateRequiredFields]
//
// See: https://google.aip.dev.
package aip
