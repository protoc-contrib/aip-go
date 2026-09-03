package aip_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/protoc-contrib/aip-go"
	testpb "github.com/protoc-contrib/aip-go/internal/testpb"
)

// validShipment is a shipment with every REQUIRED field populated.
func validShipment() *testpb.Shipment {
	return &testpb.Shipment{
		Name:        "shipments/1",
		Origin:      "Berlin",
		Destination: "Paris",
		Insured:     true,
		Fragile:     proto.Bool(false),
		Labels:      []string{"express"},
	}
}

var _ = Describe("FieldBehavior", func() {
	Describe("FieldBehaviors", func() {
		It("returns the annotated behaviors", func() {
			fields := (&testpb.Shipment{}).ProtoReflect().Descriptor().Fields()

			Expect(aip.FieldBehaviors(fields.ByName("origin"))).To(Equal([]aip.FieldBehavior{aip.Required}))
			Expect(aip.FieldBehaviors(fields.ByName("create_time"))).To(Equal([]aip.FieldBehavior{aip.OutputOnly}))
			Expect(aip.FieldBehaviors(fields.ByName("name"))).To(Equal([]aip.FieldBehavior{aip.Identifier}))
		})

		It("returns nil for an unannotated field", func() {
			fields := (&testpb.Shipment{}).ProtoReflect().Descriptor().Fields()
			Expect(aip.FieldBehaviors(fields.ByName("notes"))).To(BeNil())
		})
	})

	Describe("HasFieldBehavior", func() {
		It("matches an annotated behavior", func() {
			fields := (&testpb.Shipment{}).ProtoReflect().Descriptor().Fields()

			Expect(aip.HasFieldBehavior(fields.ByName("origin"), aip.Required)).To(BeTrue())
			Expect(aip.HasFieldBehavior(fields.ByName("origin"), aip.OutputOnly)).To(BeFalse())
			Expect(aip.HasFieldBehavior(fields.ByName("notes"), aip.Required)).To(BeFalse())
		})
	})

	Describe("ClearFields", func() {
		It("clears fields with the given behavior", func() {
			shipment := validShipment()
			shipment.CreateTime = timestamppb.New(time.Unix(1700000000, 0))
			shipment.Notes = "handle with care"

			aip.ClearFields(shipment, aip.OutputOnly)
			Expect(shipment.GetCreateTime()).To(BeNil())
			Expect(shipment.GetNotes()).To(Equal("handle with care"))
			Expect(shipment.GetOrigin()).To(Equal("Berlin"))
		})

		It("clears fields matching any of several behaviors", func() {
			shipment := validShipment()
			shipment.CreateTime = timestamppb.New(time.Unix(1700000000, 0))
			shipment.CarrierCode = "DHL"

			aip.ClearFields(shipment, aip.OutputOnly, aip.Immutable)
			Expect(shipment.GetCreateTime()).To(BeNil())
			Expect(shipment.GetCarrierCode()).To(BeEmpty())
		})

		It("descends into nested messages", func() {
			shipment := validShipment()
			shipment.Carrier = &testpb.Carrier{Name: "DHL", TrackingId: "XYZ"}

			aip.ClearFields(shipment, aip.OutputOnly)
			Expect(shipment.GetCarrier().GetTrackingId()).To(BeEmpty())
			Expect(shipment.GetCarrier().GetName()).To(Equal("DHL"))
		})

		It("descends into repeated messages", func() {
			shipment := validShipment()
			shipment.LineItems = []*testpb.LineItem{{Sku: "a"}, {Sku: "b"}}

			aip.ClearFields(shipment, aip.Required)
			Expect(shipment.GetLineItems()).To(HaveLen(2))
			for _, item := range shipment.GetLineItems() {
				Expect(item.GetSku()).To(BeEmpty())
			}
		})

		It("descends into map values", func() {
			shipment := validShipment()
			shipment.KeyedItems = map[string]*testpb.LineItem{"x": {Sku: "a", Quantity: 2}}

			aip.ClearFields(shipment, aip.Required)
			Expect(shipment.GetKeyedItems()["x"].GetSku()).To(BeEmpty())
			Expect(shipment.GetKeyedItems()["x"].GetQuantity()).To(Equal(int32(2)))
		})

		It("clears a repeated field annotated directly", func() {
			shipment := validShipment()

			aip.ClearFields(shipment, aip.Required)
			Expect(shipment.GetLabels()).To(BeEmpty())
			Expect(shipment.GetOrigin()).To(BeEmpty())
		})

		It("clears an explicit-presence field set to its zero value", func() {
			shipment := validShipment()
			Expect(shipment.Fragile).NotTo(BeNil())

			aip.ClearFields(shipment, aip.Required)
			Expect(shipment.Fragile).To(BeNil())
		})

		It("is a no-op when given no behaviors", func() {
			shipment := validShipment()
			before := proto.Clone(shipment)

			aip.ClearFields(shipment)
			Expect(proto.Equal(shipment, before)).To(BeTrue())
		})
	})

	Describe("CopyFields", func() {
		It("copies annotated fields from src to dst", func() {
			src := &testpb.Shipment{CreateTime: timestamppb.New(time.Unix(1700000000, 0))}
			dst := validShipment()

			Expect(aip.CopyFields(dst, src, aip.OutputOnly)).To(Succeed())
			Expect(dst.GetCreateTime().AsTime()).To(BeTemporally("==", time.Unix(1700000000, 0)))
			Expect(dst.GetOrigin()).To(Equal("Berlin"))
		})

		It("clears an annotated field that is unset on src", func() {
			src := &testpb.Shipment{}
			dst := validShipment()
			dst.CreateTime = timestamppb.New(time.Unix(1700000000, 0))

			Expect(aip.CopyFields(dst, src, aip.OutputOnly)).To(Succeed())
			Expect(dst.GetCreateTime()).To(BeNil())
		})

		It("returns an error on mismatched message types rather than panicking", func() {
			err := aip.CopyFields(&testpb.Shipment{}, &testpb.Carrier{}, aip.OutputOnly)
			Expect(err).To(MatchError(ContainSubstring("dst is tests.Shipment but src is tests.Carrier")))
		})

		It("is a no-op when given no behaviors", func() {
			dst := validShipment()
			before := proto.Clone(dst)

			Expect(aip.CopyFields(dst, &testpb.Shipment{})).To(Succeed())
			Expect(proto.Equal(dst, before)).To(BeTrue())
		})
	})

	Describe("ValidateRequiredFields", func() {
		It("accepts a fully populated message", func() {
			Expect(aip.ValidateRequiredFields(validShipment())).To(Succeed())
		})

		DescribeTable("rejects a missing required field",
			func(mutate func(*testpb.Shipment), path string) {
				shipment := validShipment()
				mutate(shipment)

				err := aip.ValidateRequiredFields(shipment)
				Expect(err).To(MatchError(aip.ErrMissingRequiredField))
				Expect(err).To(MatchError(ContainSubstring(path)))
			},
			Entry("string", func(s *testpb.Shipment) { s.Origin = "" }, "origin"),
			Entry("repeated", func(s *testpb.Shipment) { s.Labels = nil }, "labels"),
			Entry("presence-less bool", func(s *testpb.Shipment) { s.Insured = false }, "insured"),
			Entry("explicit-presence bool", func(s *testpb.Shipment) { s.Fragile = nil }, "fragile"),
		)

		It("treats an explicit false on an optional field as present", func() {
			shipment := validShipment()
			shipment.Fragile = proto.Bool(false)

			Expect(aip.ValidateRequiredFields(shipment)).To(Succeed())
		})

		It("validates required fields inside a nested message", func() {
			shipment := validShipment()
			shipment.Carrier = &testpb.Carrier{TrackingId: "XYZ"}

			err := aip.ValidateRequiredFields(shipment)
			Expect(err).To(MatchError(ContainSubstring("carrier.name")))
		})

		It("ignores a nested message that is absent entirely", func() {
			shipment := validShipment()
			shipment.Carrier = nil

			Expect(aip.ValidateRequiredFields(shipment)).To(Succeed())
		})

		It("validates required fields inside repeated messages", func() {
			shipment := validShipment()
			shipment.LineItems = []*testpb.LineItem{{Sku: "a"}, {Quantity: 2}}

			err := aip.ValidateRequiredFields(shipment)
			Expect(err).To(MatchError(ContainSubstring("line_items.sku")))
		})

		It("validates required fields inside map values", func() {
			shipment := validShipment()
			shipment.KeyedItems = map[string]*testpb.LineItem{"x": {Quantity: 1}}

			err := aip.ValidateRequiredFields(shipment)
			Expect(err).To(MatchError(ContainSubstring("keyed_items.sku")))
		})

		It("reports the required top-level field of a request", func() {
			err := aip.ValidateRequiredFields(&testpb.UpdateShipmentRequest{})
			Expect(err).To(MatchError(ContainSubstring("shipment")))
		})
	})

	Describe("ValidateRequiredFieldsWithMask", func() {
		It("validates nothing for a nil mask", func() {
			Expect(aip.ValidateRequiredFieldsWithMask(&testpb.Shipment{}, nil)).To(Succeed())
		})

		It("validates nothing for an empty mask", func() {
			mask := &fieldmaskpb.FieldMask{}
			Expect(aip.ValidateRequiredFieldsWithMask(&testpb.Shipment{}, mask)).To(Succeed())
		})

		It("validates a field named by the mask", func() {
			shipment := validShipment()
			shipment.Origin = ""
			mask := &fieldmaskpb.FieldMask{Paths: []string{"origin"}}

			Expect(aip.ValidateRequiredFieldsWithMask(shipment, mask)).To(MatchError(aip.ErrMissingRequiredField))
		})

		It("ignores a field the mask does not name", func() {
			shipment := validShipment()
			shipment.Origin = ""
			mask := &fieldmaskpb.FieldMask{Paths: []string{"destination"}}

			Expect(aip.ValidateRequiredFieldsWithMask(shipment, mask)).To(Succeed())
		})

		It("validates everything under the wildcard", func() {
			shipment := validShipment()
			shipment.Origin = ""
			mask := &fieldmaskpb.FieldMask{Paths: []string{"*"}}

			Expect(aip.ValidateRequiredFieldsWithMask(shipment, mask)).To(MatchError(aip.ErrMissingRequiredField))
		})

		It("validates nested fields covered by a parent path", func() {
			// The prefix match is the point: replacing `carrier` wholesale
			// means carrier's own required fields must be satisfied, even
			// though the mask never names carrier.name.
			shipment := validShipment()
			shipment.Carrier = &testpb.Carrier{TrackingId: "XYZ"}
			mask := &fieldmaskpb.FieldMask{Paths: []string{"carrier"}}

			err := aip.ValidateRequiredFieldsWithMask(shipment, mask)
			Expect(err).To(MatchError(ContainSubstring("carrier.name")))
		})

		It("does not treat a path as a prefix of an unrelated sibling", func() {
			shipment := validShipment()
			shipment.Origin = ""
			// "orig" is a string prefix of "origin" but not a path prefix.
			mask := &fieldmaskpb.FieldMask{Paths: []string{"orig"}}

			Expect(aip.ValidateRequiredFieldsWithMask(shipment, mask)).To(Succeed())
		})
	})
})
