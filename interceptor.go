package aip

import (
	"context"
	"errors"
	"uuid"

	validatepb "buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protopath"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// FieldBehaviorInterceptor is a [connect.Interceptor] that applies AIP field
// behavior on the server. Its behavior is fixed:
//
//   - On a unary RPC it clears OUTPUT_ONLY fields from the request; fills in
//     what AIP leaves to the server on each resource the request writes
//     ([ResourceWrites]) — a create's omitted `<resource>_id` becomes a UUIDv7
//     (AIP-133), an update's omitted mask becomes [ImpliedFieldMask]
//     (AIP-134), and the OUTPUT_ONLY paths of an update's explicit mask are
//     removed with [ClearFieldMask] (AIP-203); then validates the request with
//     protovalidate and reports every REQUIRED field each resource leaves unset
//     — all of them on a create, those the mask names on an update — in one
//     [connect.CodeInvalidArgument] error carrying the violations as a
//     buf.validate.Violations detail; and clears INPUT_ONLY fields from the
//     response.
//   - On a streaming RPC it clears OUTPUT_ONLY fields from every message the
//     server receives and INPUT_ONLY fields from every message it sends.
//     Streams have no standard-method shape, so nothing is filled in or
//     validated.
//
// It acts on the server only; a client passes through untouched. Clearing
// changes the messages in place, including the response the handler returns
// and every message it sends. For a different policy, build an interceptor
// from [ClearFields], [ValidateFields], [ImpliedFieldMask], [ClearFieldMask]
// and [ResourceWrites].
type FieldBehaviorInterceptor struct {
	validator protovalidate.Validator
}

var _ connect.Interceptor = (*FieldBehaviorInterceptor)(nil)

// NewFieldBehaviorInterceptor returns a [FieldBehaviorInterceptor]. It fails
// only if protovalidate's validator cannot be built.
func NewFieldBehaviorInterceptor() (*FieldBehaviorInterceptor, error) {
	validator, err := protovalidate.New()
	if err != nil {
		return nil, err
	}
	return &FieldBehaviorInterceptor{validator: validator}, nil
}

// WrapUnary implements [connect.Interceptor].
func (i *FieldBehaviorInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		request, ok := req.Any().(proto.Message)
		if req.Spec().IsClient || !ok {
			return next(ctx, req)
		}
		ClearFields(request, OutputOnly)
		implyRequest(request)
		if err := i.validate(request); err != nil {
			return nil, err
		}
		resp, err := next(ctx, req)
		if resp != nil {
			if response, ok := resp.Any().(proto.Message); ok {
				ClearFields(response, InputOnly)
			}
		}
		return resp, err
	}
}

// WrapStreamingClient implements [connect.Interceptor]: a client passes
// through untouched.
func (i *FieldBehaviorInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler implements [connect.Interceptor].
func (i *FieldBehaviorInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		return next(ctx, &fieldBehaviorConn{StreamingHandlerConn: conn})
	}
}

// fieldBehaviorConn clears what a client may not write from every message the
// server receives, and what it may not read from every message the server
// sends.
type fieldBehaviorConn struct {
	connect.StreamingHandlerConn
}

func (c *fieldBehaviorConn) Receive(message any) error {
	if err := c.StreamingHandlerConn.Receive(message); err != nil {
		return err
	}
	if m, ok := message.(proto.Message); ok {
		ClearFields(m, OutputOnly)
	}
	return nil
}

func (c *fieldBehaviorConn) Send(message any) error {
	if m, ok := message.(proto.Message); ok {
		ClearFields(m, InputOnly)
	}
	return c.StreamingHandlerConn.Send(message)
}

// implyRequest fills in what AIP leaves to the server on each resource request
// writes: a create's omitted id, an update's omitted mask. An update's explicit
// mask loses its OUTPUT_ONLY paths, which AIP-203 has a server ignore rather
// than reject; a mask left empty by that writes nothing, and is not implied
// instead. A create's name is
// the server's to build from the request's `parent` and that id (AIP-133):
// the handler reads both from the request, and the name a response returns
// comes from storage.
func implyRequest(request proto.Message) {
	for write := range ResourceWrites(request) {
		switch {
		case write.Create != nil:
			if write.Create.ID() == "" {
				write.Create.SetID(uuid.NewV7().String())
			}
		case write.Update != nil:
			if mask := write.Update.Mask(); len(mask.GetPaths()) == 0 {
				write.Update.SetMask(ImpliedFieldMask(write.Resource))
			} else {
				ClearFieldMask(write.Resource, OutputOnly, mask)
				write.Update.SetMask(mask)
			}
		}
	}
}

// validate returns nil when request passes protovalidate and leaves no
// REQUIRED field of the resources it writes unset, and otherwise one
// InvalidArgument error holding every violation, protovalidate's first, as a
// [*protovalidate.ValidationError] with a buf.validate.Violations detail. A
// rule that fails to compile or evaluate is the server's fault: Internal.
func (i *FieldBehaviorInterceptor) validate(request proto.Message) error {
	var violations []*protovalidate.Violation
	if err := i.validator.Validate(request); err != nil {
		var verr *protovalidate.ValidationError
		if !errors.As(err, &verr) {
			return connect.NewError(connect.CodeInternal, err)
		}
		violations = verr.Violations
	}
	for write := range ResourceWrites(request) {
		var mask *fieldmaskpb.FieldMask // nil on a create: every field
		if write.Update != nil {
			mask = write.Update.Mask()
		}
		var fields *FieldBehaviorError
		if errors.As(ValidateFields(write.Resource, Required, mask), &fields) {
			for _, path := range fields.Paths {
				violations = append(violations, requiredViolation(write.Path, path))
			}
		}
	}
	if len(violations) == 0 {
		return nil
	}
	verr := &protovalidate.ValidationError{Violations: violations}
	cerr := connect.NewError(connect.CodeInvalidArgument, verr)
	if detail, err := connect.NewErrorDetail(verr.ToProto()); err == nil {
		cerr.AddDetail(detail)
	}
	return cerr
}

// requiredRuleDescriptor and requiredRule are `(buf.validate.field).required`,
// the rule protovalidate names on the violation it reports for a field the
// rule finds unset.
var (
	requiredRuleDescriptor = (&validatepb.FieldRules{}).ProtoReflect().Descriptor().Fields().ByName("required")
	requiredRule           = validatepb.FieldPath_builder{
		Elements: []*validatepb.FieldPathElement{fieldPathElement(requiredRuleDescriptor)},
	}.Build()
)

// requiredViolation returns the violation protovalidate reports for a
// `required` field — rule id `required`, message `value is required` — for the
// field at path within the resource at resource, so a REQUIRED field the
// method leaves unset reads like any other violation.
func requiredViolation(resource, path protopath.Path) *protovalidate.Violation {
	var (
		elements []*validatepb.FieldPathElement
		field    protoreflect.FieldDescriptor
	)
	for _, step := range append(append(protopath.Path(nil), resource...), path...) {
		switch step.Kind() {
		case protopath.FieldAccessStep:
			field = step.FieldDescriptor()
			elements = append(elements, fieldPathElement(field))
		case protopath.ListIndexStep:
			elements[len(elements)-1].SetIndex(uint64(step.ListIndex()))
		}
	}
	return &protovalidate.Violation{
		Proto: validatepb.Violation_builder{
			Field:   validatepb.FieldPath_builder{Elements: elements}.Build(),
			Rule:    requiredRule,
			RuleId:  proto.String("required"),
			Message: proto.String("value is required"),
		}.Build(),
		FieldDescriptor: field,
		RuleValue:       protoreflect.ValueOfBool(true),
		RuleDescriptor:  requiredRuleDescriptor,
	}
}

// fieldPathElement returns the field path element of field, as protovalidate
// builds it.
func fieldPathElement(field protoreflect.FieldDescriptor) *validatepb.FieldPathElement {
	return validatepb.FieldPathElement_builder{
		FieldNumber: proto.Int32(int32(field.Number())),
		FieldName:   proto.String(string(field.Name())),
		FieldType:   descriptorpb.FieldDescriptorProto_Type(field.Kind()).Enum(),
	}.Build()
}
