package aip_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
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
})
