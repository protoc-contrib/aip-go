# aip-go

Go primitives for the [Google API Improvement Proposals](https://google.aip.dev).

The runtime companion to
[protoc-gen-go-aip](https://github.com/protoc-contrib/protoc-gen-go-aip): the
generator emits per-request parsers, and the types they return live here.

```go
import "github.com/protoc-contrib/aip-go"
```

Everything is one package, so a handler reads as `aip.ClearFields`,
`aip.ValidateFieldMask`, `aip.ResourcePattern` — names are prefixed by the AIP
concept, not by a package path.

## Status

| AIP | Concept | Status |
| --- | --- | --- |
| [122](https://google.aip.dev/122) | resource names | ✅ runtime only |
| [132](https://google.aip.dev/132#ordering) | `order_by` | not here — query layer |
| [158](https://google.aip.dev/158) | `page_token` / `page_size` | not here — query layer |
| [134](https://google.aip.dev/134) | `update_mask` validation | ✅ |
| [203](https://google.aip.dev/203) | field behavior | ✅ |
| [160](https://google.aip.dev/160) | `filter` | not here — CEL, via the generator |

## Ordering, pagination and filtering

These are deliberately not in this package. A page token or key-set cursor is
only meaningful against the resolved ordering, the path-to-column map and the
SQL that actually runs, none of which are known here, so anything parsed here
could only disagree with the query layer. That matches the Rust stack, where
[aip-rs](https://github.com/protoc-contrib/aip-rs) has no page tokens either.

Filters are plain CEL: `protoc-gen-go-aip` generates a `ParseFilter()` that
returns a `*cel.Ast`, which a query layer such as
[pgxcel](https://github.com/pgx-contrib/pgxcel) transpiles to SQL.

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

`ResourceName` is the interface every generated name type satisfies, for code
that must handle a name without knowing which one it is — logging, authz,
audit middleware. Satisfaction is structural, so generated files need no
import of this package to conform. It deliberately omits `Parent()`, which
returns a *concrete* parent type and is the main reason to use the generated
type directly when you know it.

## Development

```bash
go test ./...       # ginkgo specs
buf generate        # regenerate internal/testpb fixtures
```

## License

MIT. See [LICENSE](LICENSE) — which also records the einride/aip-go prior art
this project draws on.
