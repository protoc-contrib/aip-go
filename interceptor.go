package aip

import (
	"context"
	"errors"
	"uuid"

	validatepb "buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"buf.build/go/protovalidate"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protopath"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// NewFieldBehaviorInterceptor returns a [connect.ServerInterceptor] that
// applies AIP field behavior on the server. Its behavior is fixed:
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
//     buf.validate.Violations detail and as AIP-193's google.rpc.BadRequest;
//     and clears INPUT_ONLY fields from the response.
//   - On a streaming RPC it clears OUTPUT_ONLY fields from every message the
//     server receives and INPUT_ONLY fields from every message it sends.
//     Streams have no standard-method shape, so nothing is filled in or
//     validated.
//
// Clearing changes the messages in place, including the response the handler
// returns and every message it sends. For a different policy, build an
// interceptor from [ClearFields], [ValidateFields], [ImpliedFieldMask],
// [ClearFieldMask] and [ResourceWrites].
//
// It fails only if protovalidate's validator cannot be built.
func NewFieldBehaviorInterceptor() (connect.ServerInterceptor, error) {
	validator, err := protovalidate.New()
	if err != nil {
		return nil, err
	}
	behavior := &fieldBehavior{validator: validator}
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			return next(ctx, spec, &fieldBehaviorStream{
				ServerStream:  stream,
				fieldBehavior: behavior,
				unary:         spec.StreamType == connect.StreamTypeUnary,
			})
		}
	}, nil
}

// fieldBehavior holds what the interceptor validates requests with.
type fieldBehavior struct {
	validator protovalidate.Validator
}

// fieldBehaviorStream applies field behavior to the messages of one RPC: it
// clears what a client may not write from every message the server receives,
// and what it may not read from every message the server sends. A unary RPC's
// request is also filled in and validated as it is received, so a request that
// fails validation never reaches the handler.
type fieldBehaviorStream struct {
	connect.ServerStream
	*fieldBehavior
	unary bool
}

func (s *fieldBehaviorStream) Receive(message any) error {
	if err := s.ServerStream.Receive(message); err != nil {
		return err
	}
	request, ok := message.(proto.Message)
	if !ok {
		return nil
	}
	ClearFields(request, OutputOnly)
	if !s.unary {
		return nil
	}
	implyRequest(request)
	return s.validate(request)
}

func (s *fieldBehaviorStream) Send(message any) error {
	if response, ok := message.(proto.Message); ok {
		ClearFields(response, InputOnly)
	}
	return s.ServerStream.Send(message)
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
// [*protovalidate.ValidationError] with a buf.validate.Violations detail and a
// google.rpc.BadRequest one (see badRequest). A
// rule that fails to compile or evaluate is the server's fault: Internal,
// with the fixed message "validation failed" and the diagnostic as its cause,
// which never reaches the client.
func (b *fieldBehavior) validate(request proto.Message) error {
	var violations []*protovalidate.Violation
	if err := b.validator.Validate(request); err != nil {
		var verr *protovalidate.ValidationError
		if !errors.As(err, &verr) {
			return connect.NewError(connect.CodeInternal, "validation failed").WithCause(err)
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
	cerr := connect.NewError(connect.CodeInvalidArgument, verr.Error()).WithCause(verr)
	if detail, err := connectproto.NewErrorDetail(verr.ToProto()); err == nil {
		cerr = cerr.WithDetail(detail)
	}
	if detail, err := connectproto.NewErrorDetail(badRequest(violations)); err == nil {
		cerr = cerr.WithDetail(detail)
	}
	return cerr
}

// badRequest returns violations as AIP-193's google.rpc.BadRequest, the
// standard detail for the fields a request got wrong: a field violation per
// violation, with its field path and its message.
func badRequest(violations []*protovalidate.Violation) *errdetails.BadRequest {
	request := &errdetails.BadRequest{}
	for _, violation := range violations {
		request.FieldViolations = append(request.FieldViolations, &errdetails.BadRequest_FieldViolation{
			Field:       protovalidate.FieldPathString(violation.Proto.GetField()),
			Description: violation.Proto.GetMessage(),
		})
	}
	return request
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
