package aip

import (
	"iter"
	"slices"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protopath"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// ResourceWrite is a resource an AIP standard method request writes, as
// [ResourceWrites] yields it. Exactly one of Create and Update is set.
type ResourceWrite struct {
	// Path is the path from the request to the resource, e.g. `book`, or
	// `requests[1].book` in a batch.
	Path protopath.Path
	// Resource is the resource itself. Changes to it change the request.
	Resource proto.Message
	// Create is set when the request is a create (AIP-133).
	Create *ResourceCreate
	// Update is set when the request is an update (AIP-134).
	Update *ResourceUpdate
}

// ResourceCreate is the part of a create request (AIP-133) that identifies the
// resource it carries: the `<resource>_id` field named for the resource field,
// e.g. `book_id` for `book`. AIP-133 has the server build the resource's name
// from the request's `parent` and that id; ResourceCreate leaves the name to
// the server's own code, which reads the id and parent from the request.
type ResourceCreate struct {
	request protoreflect.Message
	id      protoreflect.FieldDescriptor
}

// ID returns the request's `<resource>_id`: empty when the client left it to
// the server, or when the request has no such field.
func (c *ResourceCreate) ID() string {
	if c.id == nil {
		return ""
	}
	return c.request.Get(c.id).String()
}

// SetID sets the request's `<resource>_id`. A request without such a field — a
// create the server names some other way — is left alone.
func (c *ResourceCreate) SetID(id string) {
	if c.id == nil {
		return
	}
	c.request.Set(c.id, protoreflect.ValueOfString(id))
}

// ResourceUpdate is the part of an update request (AIP-134) that says which of
// the resource's fields it writes: the request's `update_mask`.
type ResourceUpdate struct {
	request protoreflect.Message
	mask    protoreflect.FieldDescriptor
}

// Mask returns a copy of the request's `update_mask`, never nil: empty when the
// client left it unset or named nothing.
func (u *ResourceUpdate) Mask() *fieldmaskpb.FieldMask {
	mask := &fieldmaskpb.FieldMask{}
	if !u.request.Has(u.mask) {
		return mask
	}
	value := u.request.Get(u.mask).Message()
	paths := value.Get(value.Descriptor().Fields().ByName("paths")).List()
	for i := range paths.Len() {
		mask.Paths = append(mask.Paths, paths.Get(i).String())
	}
	return mask
}

// SetMask sets the request's `update_mask` to mask's paths.
func (u *ResourceUpdate) SetMask(mask *fieldmaskpb.FieldMask) {
	value := u.request.Mutable(u.mask).Message()
	paths := value.Mutable(value.Descriptor().Fields().ByName("paths")).List()
	paths.Truncate(0)
	for _, path := range mask.GetPaths() {
		paths.Append(protoreflect.ValueOfString(path))
	}
}

// ResourceWrites yields the resources an AIP standard method request writes, in the
// order it holds them. The request's shape, not its values, says what it is:
//
//   - A create (AIP-133) carries its resource in the singular field whose
//     message type declares google.api.resource; its ResourceWrite has Create set.
//   - An update (AIP-134) is a request whose type also has an `update_mask`
//     google.protobuf.FieldMask field; its ResourceWrite has Update set, even when the
//     mask is unset.
//   - A batch (AIP-233, AIP-234) carries such requests in a repeated
//     `requests` field; each entry yields its own ResourceWrite, under `requests[i]`.
//
// Any other request yields nothing, and so does an unset resource: there is
// nothing to fill in or validate on it, and the request's own `required` rule
// reports it. It reads the request through reflection, so a dynamic message
// works as a generated one does. A nil or typed-nil request yields nothing.
//
// ResourceWrites is what FieldBehaviorInterceptor uses to find the resources it fills
// in and validates; build a different policy on it with the field behavior
// primitives:
//
//	for write := range aip.ResourceWrites(req) {
//		var mask *fieldmaskpb.FieldMask // nil on a create: every field
//		if write.Update != nil {
//			mask = write.Update.Mask()
//		}
//		err := aip.ValidateFields(write.Resource, aip.Required, mask)
//	}
func ResourceWrites(request proto.Message) iter.Seq[ResourceWrite] {
	return func(yield func(ResourceWrite) bool) {
		if request == nil {
			return
		}
		message := request.ProtoReflect()
		if !message.IsValid() {
			return
		}
		walkWrites(message, protopath.Path{protopath.Root(message.Descriptor())}, yield)
	}
}

// walkWrites is [ResourceWrites]' walk; it reports false once yield asks to stop.
func walkWrites(request protoreflect.Message, path protopath.Path, yield func(ResourceWrite) bool) bool {
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
	field := resourceField(request.Descriptor())
	if field == nil || !request.Has(field) {
		return true
	}
	resource := request.Mutable(field).Message()
	write := ResourceWrite{
		Path:     append(slices.Clip(path), protopath.FieldAccess(field)),
		Resource: resource.Interface(),
	}
	if mask := updateMaskField(fields); mask != nil {
		write.Update = &ResourceUpdate{request: request, mask: mask}
	} else {
		write.Create = &ResourceCreate{
			request: request,
			id:      stringField(fields, protoreflect.Name(string(field.Name())+"_id")),
		}
	}
	return yield(write)
}

// resourceField returns the singular field of request whose message type
// declares google.api.resource, or nil.
func resourceField(request protoreflect.MessageDescriptor) protoreflect.FieldDescriptor {
	fields := request.Fields()
	for i := range fields.Len() {
		field := fields.Get(i)
		if field.Message() != nil && !field.IsList() && !field.IsMap() && resourceDescriptor(field.Message()) != nil {
			return field
		}
	}
	return nil
}

// resourceDescriptor returns the google.api.resource message declares, or nil.
func resourceDescriptor(message protoreflect.MessageDescriptor) *annotations.ResourceDescriptor {
	options := message.Options()
	if options == nil || !proto.HasExtension(options, annotations.E_Resource) {
		return nil
	}
	descriptor, _ := proto.GetExtension(options, annotations.E_Resource).(*annotations.ResourceDescriptor)
	return descriptor
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

// stringField returns the singular string field of fields named name, or nil.
func stringField(fields protoreflect.FieldDescriptors, name protoreflect.Name) protoreflect.FieldDescriptor {
	field := fields.ByName(name)
	if field == nil || field.Kind() != protoreflect.StringKind || field.IsList() {
		return nil
	}
	return field
}
