# aip-go

Go primitives for the [Google API Improvement Proposals](https://google.aip.dev).

The runtime companion to
[protoc-gen-go-aip](https://github.com/protoc-contrib/protoc-gen-go-aip): the
generator emits per-request parsers, and the types they return live here.

```go
import "github.com/protoc-contrib/aip-go"
```

Everything is one package, so a handler reads as `aip.ClearFields`,
`aip.IsFullReplacement`, `aip.ResourcePattern` — names are prefixed by the AIP
concept, not by a package path.

## Status

| AIP | Concept | Status |
| --- | --- | --- |
| [122](https://google.aip.dev/122) | resource names | ✅ runtime only |
| [132](https://google.aip.dev/132#ordering) | `order_by` | not here — query layer |
| [158](https://google.aip.dev/158) | `page_token` / `page_size` | not here — query layer |
| [133](https://google.aip.dev/133#user-specified-ids) | a create's omitted id, its resource's name | ✅ `ImplyResourceName` |
| [134](https://google.aip.dev/134) | `*` expansion, implied mask | ✅ `MutablePaths`, `ImpliedUpdateMask`, `ImplyUpdateMask` |
| [134](https://google.aip.dev/134) | `update_mask` validation | not here — protovalidate `field_mask.in` |
| [203](https://google.aip.dev/203) | field behavior: clearing, copying | ✅ |
| [203](https://google.aip.dev/203#immutable) | refusing a changed `IMMUTABLE` value | ✅ `ValidateImmutable` |
| [203](https://google.aip.dev/203#required) | `REQUIRED` on a resource's fields, per create or update | ✅ `ValidateRequiredFields`, `ValidateRequired` |
| [203](https://google.aip.dev/203#required) | `REQUIRED` on a request's own fields | not here — protovalidate `required` |
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
rejecting the request outright, and `CopyFields` restores the server's own. The
behaviors are re-exported, so no `genproto/annotations` import:

```go
aip.ClearFields(request.GetShipment(), aip.OutputOnly)
```

AIP-134 gives an update mask two shorthands, and a server expands each:

- **`"*"`** is full replacement: every field an update may write — not
  `OUTPUT_ONLY`, `IDENTIFIER` or `IMMUTABLE`. `IsFullReplacement` recognises it
  and `MutablePaths` lists the fields:

  ```go
  if aip.IsFullReplacement(x.GetUpdateMask()) {
          x.UpdateMask = &fieldmaskpb.FieldMask{
                  Paths: aip.MutablePaths(x.GetCollection().ProtoReflect().Descriptor()),
          }
  }
  ```

- **An omitted mask** is the *implied* mask: every writable top-level field
  that is populated on the resource, in declaration order. `ImpliedUpdateMask`
  computes it, reading the resource only. A request's `SetDefaults` fills an
  omitted mask with it — clearing first:

  ```go
  func (x *UpdateCollectionRequest) SetDefaults() {
          if len(x.GetUpdateMask().GetPaths()) == 0 {
                  aip.ClearFields(x.GetCollection(), aip.OutputOnly)
                  x.UpdateMask = aip.ImpliedUpdateMask(x.GetCollection())
          }
  }
  ```

An empty mask is **not** full replacement. A client that populated nothing
implies an empty mask — an update that writes nothing — and reading that as
`"*"` would overwrite every writable field with its zero value.

Populated is protobuf presence: a plain scalar counts when non-zero, an
`optional` one when set — even to zero — a message when set, a repeated field
or map when non-empty, a oneof member when it is the one set. Two things follow:

- **Zero values are never implied.** `false`, `0` and `""` on a plain scalar
  look exactly like unset, so a client clears such a field with an explicit
  mask — or the schema makes it `optional`.
- **Clear, then imply.** A populated message is implied as one path, so the
  update writes everything beneath it, nested `OUTPUT_ONLY` values included.
  Run `ClearFields(…, aip.OutputOnly)` first, as above.

`MutablePaths` also keeps a hand-written column map honest — a test that every
writable path has a column fails when a field is added without one.

**A changed `IMMUTABLE` value is refused, not dropped.** Neither the implied
mask nor `*` includes an `IMMUTABLE` field, so on their own they would discard a
new value without a word. AIP-203 has the service reject it instead;
`ValidateImmutable` names the fields an update changes against the stored
resource, in an `*aip.ImmutableError` — an echoed-back value is not a change, and an unset field is not part
of the request. A list that is also `UNORDERED_LIST` is compared without regard
to order, since the service may have reordered it:

```go
stored, err := repository.Get(ctx, name)
// ...
if err := aip.ValidateImmutable(stored, req.GetCollection()); err != nil {
	return status.Error(codes.InvalidArgument, err.Error()) // "immutable fields cannot change: sku"
}
```

**`REQUIRED` depends on the method.** AIP-203 has a create set every `REQUIRED`
field, while a partial update may omit one its mask does not name — but must set
one it names, or the update would clear it. `ValidateRequiredFields` answers both:
a nil mask is a create, any other mask an update. A set nested message is
checked in turn, since its annotations are independent of its parent's; an
unset one is not:

```go
// create
err := aip.ValidateRequiredFields(req.GetCollection(), nil)
// update, after an omitted mask is implied and "*" expanded
err := aip.ValidateRequiredFields(req.GetCollection(), req.GetUpdateMask())
if err != nil {
	return status.Error(codes.InvalidArgument, err.Error()) // "required fields are unset: display_name"
}
```

Unset is protobuf presence, as for the implied mask: an explicit `false` on an
`optional bool` is set, and a plain `bool` must be `true`.

**Whole requests, not just resources.** Which resource a request writes, and
whether it is a create or an update, are AIP conventions too, so a server need
not restate them per request. `ValidateRequired`, `ImplyResourceName`,
`ImplyUpdateMask` and `ImplyRequest`, which runs both implications, take the
request itself and recognise:

- **a create** (AIP-133): the resource is the singular field whose message type
  declares `google.api.resource`; every `REQUIRED` field is checked, and an
  omitted `<resource>_id` is assigned with the caller's `newID` and the
  resource named from its pattern and the request's `parent`;
- **an update** (AIP-134): the request's type also has an `update_mask`
  `google.protobuf.FieldMask` field; only the fields the mask names are
  checked. The schema decides, never the value: an unset mask names nothing,
  it does not turn an update into a create;
- **a batch** (AIP-233, AIP-235): a repeated `requests` field of such requests;
  each entry is handled as its own request.

Any other request is left alone. One interceptor then serves every RPC, in this
order:

```go
aip.ClearFields(req, aip.OutputOnly) // server-owned values never reach the mask
aip.ImplyRequest(req, newID)         // a create's id and name, an omitted or empty mask
err := aip.ValidateRequired(req)     // "required fields are unset: requests[1].book.title"

var required *aip.RequiredError
if errors.As(err, &required) {
	for _, path := range required.Paths {
		// path.String(): "(acme.v1.BatchCreateBooksRequest).requests[1].book.title"
	}
}
```

The errors hold `protopath.Path`s rather than strings, so a batch entry's index
survives and a caller can build a protovalidate field path from the steps. The
functions read the request through reflection, so a `dynamicpb` message works as
a generated one does.

**Stricter than AIP-161, on purpose.** AIP-161 has a mask that names an
`OUTPUT_ONLY` field ignored. Here `field_mask.in` lists only the writable
fields, so protovalidate rejects such a mask with `InvalidArgument` — a clearer
answer than a silent no-op, and safer than trusting every caller to know which
paths are dropped. [protoc-gen-aip-lint](https://github.com/protoc-contrib/protoc-gen-aip-lint)'s
`update-mask-writable-fields` rule keeps that list equal to the fields
`google.api.field_behavior` leaves writable.

**Validation is split with protovalidate.** A request's own fields — `parent`,
the resource field itself — every value's format, and whether an `update_mask`
names real fields are protovalidate's rules: `(buf.validate.field).required`,
`min_len`, `field_mask.in`. What protovalidate cannot do is tell a create from
an update. A resource message is shared by both requests, so a rule on its
field applies to both, and `REQUIRED` means something different in each. The
workaround was a CEL rule per field per request — `this.display_name != ''` on
create, `!('display_name' in this.update_mask.paths) || …` on update — restating
`google.api.field_behavior` in strings no lint can check. `ValidateRequiredFields`
reads the annotation instead, so a resource field marked `REQUIRED` carries no
`buf.validate` `required` of its own. This reverses the line this package drew
when it removed the old `ValidateRequiredFields` and `ValidateRequiredFieldsWithMask`:
with the CEL rules gone there is no second implementation for it to disagree
with. Nor can protovalidate *expand* a mask's shorthands, which is why
`IsFullReplacement`, `MutablePaths` and `ImpliedUpdateMask` are here.

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
