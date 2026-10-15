# aip-go

Go primitives for the [Google API Improvement Proposals](https://google.aip.dev).

The runtime companion to
[protoc-gen-go-aip](https://github.com/protoc-contrib/protoc-gen-go-aip): the
generator emits per-request parsers, and the types they return live here.

```go
import "github.com/protoc-contrib/aip-go"
```

Everything is one package, so a handler reads as `aip.ClearFields`,
`aip.ValidateFields`, `aip.ResourcePattern` — names are prefixed by the AIP
concept, not by a package path.

## Status

| AIP | Concept | Status |
| --- | --- | --- |
| [122](https://google.aip.dev/122) | resource names | ✅ runtime only |
| [132](https://google.aip.dev/132#ordering) | `order_by` | not here — query layer |
| [158](https://google.aip.dev/158) | `page_token` / `page_size` | not here — query layer |
| [134](https://google.aip.dev/134) | implied field mask | ✅ `ImpliedFieldMask` |
| [134](https://google.aip.dev/134) | `update_mask` validation | not here — protovalidate `field_mask.in` |
| [203](https://google.aip.dev/203) | field behavior: clearing any kind | ✅ `ClearFields` |
| [203](https://google.aip.dev/203#output-only) | `OUTPUT_ONLY` paths in an update mask, ignored | ✅ `ClearFieldMask` |
| [203](https://google.aip.dev/203#required) | fields of a kind left unset (`REQUIRED`), per create or update | ✅ `ValidateFields` |
| [203](https://google.aip.dev/203#required) | `REQUIRED` on a request's own fields | not here — protovalidate `required` |
| [160](https://google.aip.dev/160) | `filter` | not here — CEL, via the generator |
| [133](https://google.aip.dev/133), [134](https://google.aip.dev/134), [233](https://google.aip.dev/233), [234](https://google.aip.dev/234) | standard-method request shapes | ✅ `ResourceWrites` |
| — | Connect interceptor applying all of the above | ✅ `NewFieldBehaviorInterceptor` |

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

The field behavior functions are primitives: each works on **any message** and
**any kind** of `google.api.field_behavior`, at any depth. Which kinds a service
applies, and where, is its own policy — including going against AIP. The kinds
are re-exported as `aip.FieldBehaviorKind`, so no `genproto/annotations` import.
A field's annotation reads as a set, in the manner of `protoreflect`:

```go
behaviors := aip.FieldBehaviorsOf(fd)       // e.g. [REQUIRED IMMUTABLE]
behaviors.Has(aip.Required)                 // true
behaviors.HasAny(aip.OutputOnly, aip.Identifier, aip.Immutable)
for kind := range behaviors.All() { /* enum order */ }
```

| Function | Does |
| --- | --- |
| `ClearFields(m, kind)` | unsets every field annotated `kind` |
| `ValidateFields(m, kind, mask)` | a `*aip.FieldBehaviorError` naming the fields annotated `kind` that are unset — all of them for a nil mask, only those the mask names otherwise |
| `ImpliedFieldMask(m)` | the field mask AIP-134 implies when an update omits one |
| `ClearFieldMask(m, kind, mask)` | removes from `mask` the paths that name, or pass through, a field annotated `kind` |
| `FieldBehaviorsOf(fd)` | the field's annotation, as a `FieldBehaviorSet` — `Has`, `HasAny`, `All`, `String` — decoded once per descriptor and cached; safe for concurrent use |

AIP-203 defines three uses, and a server typically makes them in this order:

```go
aip.ClearFields(req, aip.OutputOnly) // server-owned values never reach the mask

// update: an omitted mask is implied from the writable fields set, and an
// explicit one ignores the OUTPUT_ONLY fields it names
if len(req.GetUpdateMask().GetPaths()) == 0 {
	req.UpdateMask = aip.ImpliedFieldMask(req.GetBook())
} else {
	aip.ClearFieldMask(req.GetBook(), aip.OutputOnly, req.GetUpdateMask())
}

// REQUIRED: a nil mask (create) checks every field, a mask only the ones it names
if err := aip.ValidateFields(req.GetBook(), aip.Required, req.GetUpdateMask()); err != nil {
	return status.Error(codes.InvalidArgument, err.Error()) // "REQUIRED fields are unset: title"
}

aip.ClearFields(resp, aip.InputOnly) // never returned
```

Which field of a request holds the resource, and whether a request is a create
or an update, is what [`ResourceWrites`](#request-shapes-and-the-connect-interceptor)
works out; these primitives take the resource and mask from the caller.

**`ValidateFields` and the mask.** A create must set every `REQUIRED` field; a
partial update may omit one its mask does not name, but must set one it names,
or the update would clear it. A path naming a message field covers everything
beneath it; a nested path such as `carrier.name` checks just that field, as
unset when `carrier` is. A set nested message is checked in turn, since its
annotations are independent of its parent's; an unset one is not. A repeated or
map field is checked itself but never descended into, since a mask path cannot
address its elements. `"*"` is not expanded. The error holds `protopath.Path`s,
so a caller can build a protovalidate field path from the steps:

```go
var fields *aip.FieldBehaviorError
if errors.As(err, &fields) {
	for _, path := range fields.Paths {
		// path.String(): "(acme.v1.Book).author.name"
	}
}
```

**Unset is protobuf presence**, for both `ValidateFields` and the implied mask:
a plain scalar counts when non-zero, an `optional` one when set — even to zero —
a message when set, a repeated field or map when non-empty, a oneof member when
it is the one set. Two things follow:

- **Zero values are never implied.** `false`, `0` and `""` on a plain scalar
  look exactly like unset, so a client clears such a field with an explicit
  mask — or the schema makes it `optional`. A `REQUIRED` plain `bool` must be
  `true`.
- **Clear, then imply.** A populated message is implied as one path, so the
  update writes everything beneath it, nested `OUTPUT_ONLY` values included.
  Run `ClearFields(…, aip.OutputOnly)` first, as above.

**Validation is split with protovalidate.** A request's own fields — `parent`,
the resource field itself — every value's format, and whether an `update_mask`
names real fields are protovalidate's rules: `(buf.validate.field).required`,
`min_len`, `field_mask.in`. What protovalidate cannot do is tell a create from
an update: a resource message is shared by both requests, so `REQUIRED` on its
fields is `ValidateFields`', reading the annotation instead.

## Request shapes and the Connect interceptor

`ResourceWrites(req)` yields the resources an AIP standard method request writes. The
request's shape, not its values, says what it is: a create (AIP-133) carries its
resource in the singular field whose type declares `google.api.resource`; an
update (AIP-134) also has an `update_mask` `google.protobuf.FieldMask` field, so
an unset mask names nothing rather than turning it into a create; a batch
(AIP-233, AIP-234) carries such requests in `requests`, each yielded under
`requests[i]`. Each `ResourceWrite` has the resource, its path, and either a
`ResourceCreate` (the `<resource>_id`) or a `ResourceUpdate` (the
`update_mask`).

`NewFieldBehaviorInterceptor` returns a `connect.ServerInterceptor`
([connect-go v2](https://github.com/connectrpc/connect-go)) applying all of it on
the server, with fixed behavior:

| Every message received | Every message sent |
| --- | --- |
| clear `OUTPUT_ONLY`; on a create, an omitted `<resource>_id` becomes a UUIDv7; on an update, an omitted mask becomes `ImpliedFieldMask` and an explicit one drops its `OUTPUT_ONLY` paths (`ClearFieldMask`); then protovalidate plus `REQUIRED` (every field on a create, the masked ones on an update), as one `InvalidArgument` with a `buf.validate.Violations` detail and a `google.rpc.BadRequest` | clear `INPUT_ONLY` |

It acts on every message of every RPC, unary or streamed. A message that is not
a standard-method request — a resource streamed on its own — has nothing filled
in or checked for `REQUIRED`; only clearing and its protovalidate rules apply.
The error is returned from the handler's `Receive`, so a message that fails
validation never reaches it: a streaming handler returns any `Receive` error
other than `io.EOF`.

**A create's name is the handler's to build.** AIP-133 has the server form the
name from the request's `parent` and the id; the interceptor only fills in an
omitted id. The handler reads both from the request — e.g. the generated
`ParseItemID()` and `ParseParent()` — and the `name` a response returns comes
from storage, so the resource's `name` and `parent` on the request are left as
they arrive. It is a server interceptor, so it never acts on a client. Clearing
changes messages in place, including the response a handler returns and every
message it sends.

```go
fields, err := aip.NewFieldBehaviorInterceptor()
if err != nil {
	return err
}
server := connect.NewServer(fields)
acmev1connect.RegisterBookServiceHandler(server, svc)
connecthttp.Mount(mux, server)
```

A `REQUIRED` field left unset reads exactly as protovalidate's own `required`
violation — rule id `required`, message `value is required` — in the same error
as the request's protovalidate violations. That error's message is fixed, "The
request contains invalid parameters", and names no field: AIP-193 keeps what
varies in the details. It carries the violations twice: as
`buf.validate.Violations`, with protovalidate's rule ids, and as AIP-193's
`google.rpc.BadRequest` — a field violation per violation, its path and message
(`book.title`, `value is required`) — the detail any AIP client reads. For a different policy — other kinds, IMMUTABLE cleared on update,
another id scheme — build an interceptor from `ClearFields`, `ValidateFields`,
`ImpliedFieldMask`, `ClearFieldMask` and `ResourceWrites`; `ResourceCreate.SetID` sets an id of
your own scheme.

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
