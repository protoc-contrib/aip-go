# aip-go

Go primitives for the [Google API Improvement Proposals](https://google.aip.dev).

The runtime companion to
[protoc-gen-go-aip](https://github.com/protoc-contrib/protoc-gen-go-aip): the
generator emits per-request parsers, and the types they return live here.

```go
import "github.com/protoc-contrib/aip-go"
```

Everything is one package, so a handler reads as `aip.OrderBy`,
`aip.PageToken`, `aip.Cursor` — names are prefixed by the AIP concept, not by
a package path.

## Status

| AIP | Concept | Status |
| --- | --- | --- |
| [122](https://google.aip.dev/122) | resource names | ✅ runtime only |
| [132](https://google.aip.dev/132#ordering) | `order_by` | ✅ |
| [158](https://google.aip.dev/158) | `page_token` / `page_size` | ✅ |
| [134](https://google.aip.dev/134) | `update_mask` validation | ✅ |
| [203](https://google.aip.dev/203) | field behavior | ✅ |
| [160](https://google.aip.dev/160) | `filter` | planned |

## Usage

```go
orderBy, err := aip.ParseOrderBy(request)
if err != nil {
        return nil, connect.NewError(connect.CodeInvalidArgument, err)
}
if err := orderBy.ValidateForPaths("title", "create_time", "name"); err != nil {
        return nil, connect.NewError(connect.CodeInvalidArgument, err)
}

token, err := aip.ParsePageToken(request)
if err != nil {
        return nil, connect.NewError(connect.CodeInvalidArgument, err)
}

books := query(token, orderBy, request.GetPageSize())

var nextPageToken string
if len(books) == int(request.GetPageSize()) {
        token, err = token.NextCursor(books[len(books)-1], orderBy.Paths()...)
        if err != nil {
                return nil, err
        }
        if nextPageToken, err = token.Encode(); err != nil {
                return nil, err
        }
}
```

## Pagination

`PageToken` supports both AIP-158 styles. Pick one per List method:

- **Offset** — `token.Next(request)` advances by `page_size`. Simple, but
  skipping rows gets more expensive with each page and concurrent writes shift
  rows across page boundaries.
- **Key-set** — `token.NextCursor(lastRow, orderBy.Paths()...)` records the
  sort-key tuple of the last row. Constant cost per page and stable under
  concurrent writes, provided the trailing `order_by` field is unique.

`Cursor` values are restricted to a fixed set of types (`nil`, `bool`,
`string`, `[]byte`, the sized integers, floats, `time.Time`, `time.Duration`)
and are encoded with an explicit type tag per value. An unsupported value is
an error from `Encode`, never a silently truncated token.

Cursor extraction resolves dotted paths (`author.name`), unwraps
`google.protobuf.Timestamp`, `Duration` and the wrapper types, and yields
`nil` for an unset message or `optional` field so the query layer can compare
it as SQL `NULL` rather than as a zero value.

Tokens are versioned, `base64url` without padding, and carry a CRC-32 of the
request fields that must not change between pages. A client that swaps its
`filter` mid-page gets `ErrChecksumMismatch`; a corrupt token gets
`ErrMalformedPageToken`. Both map to `InvalidArgument`.

## Field behavior and field masks

`ClearFields` drops the values a client should not be setting, rather than
rejecting the request outright; `ValidateRequiredFields` then checks what is
left. The behaviors are re-exported, so no `genproto/annotations` import:

```go
aip.ClearFields(request.GetShipment(), aip.OutputOnly)
if err := aip.ValidateRequiredFields(request.GetShipment()); err != nil {
        return nil, connect.NewError(connect.CodeInvalidArgument, err)
}
```

For a partial update, validate against the mask instead. Coverage is by
prefix, so a mask of `["carrier"]` also validates the required fields nested
beneath `carrier` — replacing a subtree means the whole subtree must be valid:

```go
if err := aip.ValidateFieldMask(request.GetUpdateMask(), request.GetShipment()); err != nil {
        return nil, connect.NewError(connect.CodeInvalidArgument, err)
}
if err := aip.ValidateRequiredFieldsWithMask(request.GetShipment(), request.GetUpdateMask()); err != nil {
        return nil, connect.NewError(connect.CodeInvalidArgument, err)
}
```

A field with explicit presence — a message, an `optional` scalar, a oneof
member — is judged by presence, so an explicit `false` satisfies a REQUIRED
`optional bool`. A presence-less proto3 scalar has no way to distinguish unset
from zero, so a REQUIRED one must be non-zero; declare it `optional` if zero
is a legitimate value.

## Resource names

Resource name *types* stay generated — `protoc-gen-go-aip` emits a concrete
`BookName{PublisherID, BookID string}` with typed `Parent()` and builders, and
that type safety is the whole point. What lives here is the machinery behind
those methods, so a fix to segment walking ships as a `go.mod` bump instead of
a regen across every consumer:

```go
var bookNamePattern = aip.MustCompileResourcePattern("publishers/{publisher}/books/{book}")

func ParseBookName(s string) (BookName, error) {
        var out BookName
        if err := bookNamePattern.Scan(s, &out.PublisherID, &out.BookID); err != nil {
                return BookName{}, err
        }
        return out, nil
}

func (n BookName) String() string  { return bookNamePattern.Format(n.PublisherID, n.BookID) }
func (n BookName) Validate() error { return bookNamePattern.Validate(n.PublisherID, n.BookID) }
```

`Scan` walks the name in place and allocates nothing — it is faster than the
`strings.Split` it replaces (42ns/0 allocs vs 51ns/1 alloc on an Apple M-series
machine). `Format` costs about 10ns more than inlined concatenation for the
same single allocation.

There is deliberately no runtime parser taking a pattern string as public API:
callers get `ParseBookName(s)`, never `Scan(s, "publishers/{publisher}/...")`.

## Development

```bash
go test ./...       # ginkgo specs
buf generate        # regenerate internal/testpb fixtures
```

## License

MIT. See [LICENSE](LICENSE) — which also records the einride/aip-go prior art
this project draws on.
