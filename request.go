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

// MissingRequiredRequestPaths returns the REQUIRED fields that request, an AIP
// standard method request, leaves unset, as paths from request, in the order
// the request holds them:
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
// Any other request yields nil, and so does an unset resource, which the
// request's own `required` rule reports. It is [MissingRequiredPaths] applied
// to each resource the request writes, with the same rules for nested
// messages, presence and "*". An update that omitted its mask names nothing
// until [ImplyUpdateMask] fills it in, so a server runs, in order:
//
//	aip.ClearFields(req, aip.OutputOnly)
//	aip.ImplyUpdateMask(req)
//	for _, path := range aip.MissingRequiredRequestPaths(req) {
//		// path.String(): "(acme.v1.BatchCreateBooksRequest).requests[1].book.title"
//	}
//
// A nil or typed-nil request yields nil.
//
// See: https://google.aip.dev/203#required.
func MissingRequiredRequestPaths(request proto.Message) []protopath.Path {
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
		for _, name := range MissingRequiredPaths(w.request.Get(w.resource).Message().Interface(), mask) {
			missing = append(missing, fieldPath(at, w.resource.Message(), name))
		}
	}
	return missing
}

// ImplyUpdateMask sets, on an update request (AIP-134) whose update_mask is
// unset or names nothing, and on each such entry of a batch request (AIP-235),
// the mask [ImpliedUpdateMask] implies from its resource. A mask the client
// sent is kept, and a create, an update without a resource, or any other
// request is left untouched. Requests are recognised as
// [MissingRequiredRequestPaths] recognises them.
//
// Clear OUTPUT_ONLY fields first, so the implied mask never names a value a
// client tried to set on one:
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
