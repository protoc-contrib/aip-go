package aip

import (
	"iter"
	"slices"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protopath"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// ValidateRequired returns a [*RequiredError] naming the REQUIRED fields that
// request, an AIP standard method request, leaves unset, as paths from
// request, in the order the request holds them; nil when there are none:
//
//   - A create (AIP-133) carries its resource in the singular field whose
//     message type declares google.api.resource; every REQUIRED field of the
//     resource is checked.
//   - An update (AIP-134) is a request whose type also has an `update_mask`
//     google.protobuf.FieldMask field; only the fields the mask names are
//     checked, so a partial update may omit a REQUIRED field but not clear one.
//     Which method it is comes from the schema, never the value: an unset mask
//     names nothing, it does not turn an update into a create.
//   - A batch (AIP-233, AIP-235) carries such requests in a repeated `requests`
//     field; each entry is checked as its own request, under `requests[i]`.
//
// Any other request is valid, and so is an unset resource, which the request's
// own `required` rule reports. It is [ValidateRequiredFields] applied to each
// resource the request writes, with the same rules for nested messages,
// presence and "*". An update that omitted its mask names nothing until
// [ImplyRequest] fills it in, so a server runs, in order:
//
//	aip.ClearFields(req, aip.OutputOnly)
//	aip.ImplyRequest(req, newID)
//	if err := aip.ValidateRequired(req); err != nil {
//		// err.Error(): "required fields are unset: requests[1].book.title"
//	}
//
// A nil or typed-nil request is valid.
//
// See: https://google.aip.dev/203#required.
func ValidateRequired(request proto.Message) error {
	message, ok := reflectRequest(request)
	if !ok {
		return nil
	}
	var missing []protopath.Path
	for w := range writes(message, protopath.Path{protopath.Root(message.Descriptor())}) {
		if !w.request.Has(w.resource) {
			continue
		}
		var mask *fieldmaskpb.FieldMask // nil: a create
		if w.mask != nil {
			mask = maskOf(w.request, w.mask)
		}
		at := append(slices.Clip(w.path), protopath.FieldAccess(w.resource))
		missing = append(missing, requiredPaths(w.request.Get(w.resource).Message(), at, mask)...)
	}
	return newRequiredError(missing)
}

// ImplyRequest fills in what AIP lets a client leave out of a create, update
// or batch request: a create's resource name ([ImplyResourceName]), then an
// update's omitted mask ([ImplyUpdateMask]). Clear OUTPUT_ONLY fields first,
// and validate after:
//
//	aip.ClearFields(req, aip.OutputOnly)
//	aip.ImplyRequest(req, func() string { return uuid.Must(uuid.NewV7()).String() })
//	err := aip.ValidateRequired(req)
//
// A nil or typed-nil request is left alone.
func ImplyRequest(request proto.Message, newID func() string) {
	ImplyResourceName(request, newID)
	ImplyUpdateMask(request)
}

// ImplyResourceName names the resource a create request (AIP-133) carries, and
// the one each create entry of a batch (AIP-233) carries, as AIP-133 leaves to
// the server:
//
//   - an omitted `<resource>_id` — the request field named for the resource
//     field, e.g. `book_id` for `book` — becomes newID();
//   - the resource's `parent` field, when it has one, is copied from the
//     request's `parent`;
//   - the resource's `name` is built from the first pattern its
//     google.api.resource declares, replacing any name the client sent: a
//     top-level pattern's collection and the id (`books/{book}` gives
//     `books/<id>`), or a nested pattern's last collection under the request's
//     `parent` (`books/{book}/chapters/{chapter}` gives `<parent>/chapters/<id>`).
//
// An id the client sent is used as is: whether it is well formed is the
// request's protovalidate rules to check. A request without a `<resource>_id`
// field, an unset resource, an update — whose name selects its target — and
// any other request are left untouched; requests are recognised as
// [ValidateRequired] recognises them. newID is the caller's, so the id scheme
// is too:
//
//	aip.ImplyResourceName(req, func() string { return uuid.Must(uuid.NewV7()).String() })
//
// A nil or typed-nil request is left alone.
//
// See: https://google.aip.dev/133#user-specified-ids.
func ImplyResourceName(request proto.Message, newID func() string) {
	message, ok := reflectRequest(request)
	if !ok {
		return
	}
	for w := range writes(message, nil) {
		if w.mask != nil || !w.request.Has(w.resource) {
			continue // an update, or nothing to name
		}
		fields := w.request.Descriptor().Fields()
		id := fields.ByName(protoreflect.Name(string(w.resource.Name()) + "_id"))
		if id == nil || id.Kind() != protoreflect.StringKind || id.IsList() {
			continue // a create the server names some other way
		}
		if w.request.Get(id).String() == "" {
			w.request.Set(id, protoreflect.ValueOfString(newID()))
		}
		resource := w.request.Mutable(w.resource).Message()
		parent := ""
		if field := stringField(fields, "parent"); field != nil {
			parent = w.request.Get(field).String()
			if field := stringField(resource.Descriptor().Fields(), "parent"); field != nil {
				resource.Set(field, protoreflect.ValueOfString(parent))
			}
		}
		if field := stringField(resource.Descriptor().Fields(), "name"); field != nil {
			name := resourceName(w.resource.Message(), parent, w.request.Get(id).String())
			resource.Set(field, protoreflect.ValueOfString(name))
		}
	}
}

// resourceName builds the name of a resource of type resource from the first
// pattern its google.api.resource declares: the collection before the last
// variable and id for a top-level pattern, or that collection under parent for
// a nested one.
func resourceName(resource protoreflect.MessageDescriptor, parent, id string) string {
	descriptor, _ := proto.GetExtension(resource.Options(), annotations.E_Resource).(*annotations.ResourceDescriptor)
	patterns := descriptor.GetPattern()
	if len(patterns) == 0 {
		return ""
	}
	segments := strings.Split(patterns[0], "/")
	if len(segments) < 2 {
		return ""
	}
	collection := strings.Join(segments[:len(segments)-1], "/")
	if strings.Contains(collection, "{") {
		collection = parent + "/" + segments[len(segments)-2]
	}
	return collection + "/" + id
}

// stringField returns the singular string field of fields named name, or nil.
func stringField(fields protoreflect.FieldDescriptors, name protoreflect.Name) protoreflect.FieldDescriptor {
	field := fields.ByName(name)
	if field == nil || field.Kind() != protoreflect.StringKind || field.IsList() {
		return nil
	}
	return field
}

// ImplyUpdateMask sets, on an update request (AIP-134) whose update_mask is
// unset or names nothing, and on each such entry of a batch request (AIP-235),
// the mask [ImpliedUpdateMask] implies from its resource. A mask the client
// sent is kept, and a create, an update without a resource, or any other
// request is left untouched. Requests are recognised as
// [ValidateRequired] recognises them.
//
// Clear OUTPUT_ONLY fields first, so the implied mask never names a value a
// client tried to set on one; [ImplyRequest] runs it with [ImplyResourceName]:
//
//	aip.ClearFields(req, aip.OutputOnly)
//	aip.ImplyUpdateMask(req)
//
// A nil or typed-nil request is left alone.
//
// See: https://google.aip.dev/134#field-masks.
func ImplyUpdateMask(request proto.Message) {
	message, ok := reflectRequest(request)
	if !ok {
		return
	}
	for w := range writes(message, nil) {
		if w.mask == nil || !w.request.Has(w.resource) || len(maskOf(w.request, w.mask).GetPaths()) > 0 {
			continue
		}
		implied := ImpliedUpdateMask(w.request.Get(w.resource).Message().Interface())
		paths := w.request.Mutable(w.mask).Message()
		list := paths.Mutable(paths.Descriptor().Fields().ByName("paths")).List()
		list.Truncate(0)
		for _, path := range implied.GetPaths() {
			list.Append(protoreflect.ValueOfString(path))
		}
	}
}

// write is a resource an AIP standard method request writes: the request (or
// batch entry) holding it and the path to that request, the field holding the
// resource, and the request's update_mask field — nil on a create.
type write struct {
	path     protopath.Path
	request  protoreflect.Message
	resource protoreflect.FieldDescriptor
	mask     protoreflect.FieldDescriptor
}

// writes yields the resource a create or update request writes, or, for a
// batch request, the one each entry of `requests` writes, with the entry's path
// under path. It is the one place the AIP-133, 134, 233 and 235 request shapes
// are recognised.
func writes(request protoreflect.Message, path protopath.Path) iter.Seq[write] {
	return func(yield func(write) bool) {
		walkWrites(request, path, yield)
	}
}

// walkWrites is [writes]' walk; it reports false once yield asks to stop.
func walkWrites(request protoreflect.Message, path protopath.Path, yield func(write) bool) bool {
	fields := request.Descriptor().Fields()
	if entries := fields.ByName("requests"); entries != nil && entries.IsList() && entries.Message() != nil {
		list := request.Get(entries).List()
		for i := range list.Len() {
			at := append(slices.Clip(path), protopath.FieldAccess(entries), protopath.ListIndex(i))
			if !walkWrites(list.Get(i).Message(), at, yield) {
				return false
			}
		}
		return true
	}
	resource := resourceField(request.Descriptor())
	if resource == nil {
		return true
	}
	return yield(write{path: path, request: request, resource: resource, mask: updateMaskField(fields)})
}

// resourceField returns the singular field of request whose message type
// declares google.api.resource, or nil.
func resourceField(request protoreflect.MessageDescriptor) protoreflect.FieldDescriptor {
	fields := request.Fields()
	for i := range fields.Len() {
		field := fields.Get(i)
		if field.Message() != nil && !field.IsList() && !field.IsMap() && isResource(field.Message()) {
			return field
		}
	}
	return nil
}

// isResource reports whether message declares google.api.resource.
func isResource(message protoreflect.MessageDescriptor) bool {
	options := message.Options()
	return options != nil && proto.HasExtension(options, annotations.E_Resource)
}

// updateMaskField returns the `update_mask` google.protobuf.FieldMask field of
// a request, or nil.
func updateMaskField(fields protoreflect.FieldDescriptors) protoreflect.FieldDescriptor {
	field := fields.ByName("update_mask")
	if field == nil || field.IsList() || field.Message() == nil ||
		field.Message().FullName() != "google.protobuf.FieldMask" {
		return nil
	}
	return field
}

// maskOf returns the mask request holds in field, empty when it is unset. It
// reads the mask through reflection, so a dynamic message works as a generated
// one does.
func maskOf(request protoreflect.Message, field protoreflect.FieldDescriptor) *fieldmaskpb.FieldMask {
	mask := &fieldmaskpb.FieldMask{}
	value := request.Get(field).Message()
	paths := value.Get(value.Descriptor().Fields().ByName("paths")).List()
	for i := range paths.Len() {
		mask.Paths = append(mask.Paths, paths.Get(i).String())
	}
	return mask
}

// fieldPath returns at extended with the fields the dotted name walks through,
// starting from message.
func fieldPath(at protopath.Path, message protoreflect.MessageDescriptor, name string) protopath.Path {
	path := slices.Clip(at)
	for segment := range strings.SplitSeq(name, ".") {
		field := message.Fields().ByName(protoreflect.Name(segment))
		path = append(path, protopath.FieldAccess(field))
		message = field.Message()
	}
	return path
}

// reflectRequest returns request's reflection, and false for a nil or typed-nil
// request.
func reflectRequest(request proto.Message) (protoreflect.Message, bool) {
	if request == nil {
		return nil, false
	}
	message := request.ProtoReflect()
	return message, message.IsValid()
}
