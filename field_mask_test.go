package aip_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/protoc-contrib/aip-go"
	testpb "github.com/protoc-contrib/aip-go/internal/testpb"
)

var _ = Describe("FieldMask", func() {
	Describe("IsFullReplacement", func() {
		DescribeTable("classifies the mask",
			func(mask *fieldmaskpb.FieldMask, expected bool) {
				Expect(aip.IsFullReplacement(mask)).To(Equal(expected))
			},
			// Absent or empty is AIP-134's implied mask, not full replacement.
			Entry("nil mask", nil, false),
			Entry("empty mask", &fieldmaskpb.FieldMask{}, false),
			Entry("wildcard", &fieldmaskpb.FieldMask{Paths: []string{"*"}}, true),
			Entry("single field", &fieldmaskpb.FieldMask{Paths: []string{"origin"}}, false),
			Entry("wildcard with another path", &fieldmaskpb.FieldMask{Paths: []string{"*", "origin"}}, false),
		)

		It("does not read an empty implied mask as full replacement", func() {
			// A client that populated nothing implies an empty mask: an update
			// that writes nothing, never one that overwrites every field.
			Expect(aip.IsFullReplacement(aip.ImpliedUpdateMask(&testpb.Shipment{}))).To(BeFalse())
		})
	})

	Describe("MutablePaths", func() {
		It("names every writable field, in declaration order", func() {
			// name is IDENTIFIER, create_time OUTPUT_ONLY, carrier_code IMMUTABLE.
			desc := (&testpb.Shipment{}).ProtoReflect().Descriptor()
			Expect(aip.MutablePaths(desc)).To(Equal([]string{
				"origin", "destination", "notes", "carrier", "line_items",
				"keyed_items", "insured", "fragile", "labels",
			}))
		})

		It("leaves out an OUTPUT_ONLY field of a nested message's own", func() {
			desc := (&testpb.Carrier{}).ProtoReflect().Descriptor()
			Expect(aip.MutablePaths(desc)).To(Equal([]string{"name"}))
		})

		It("contains every path ImpliedUpdateMask can return", func() {
			shipment := &testpb.Shipment{
				Name: "shipments/1", Origin: "Berlin", Destination: "Paris",
				CreateTime: timestamppb.Now(), Notes: "n", CarrierCode: "DHL",
				Carrier:    &testpb.Carrier{Name: "DHL"},
				LineItems:  []*testpb.LineItem{{Sku: "a"}},
				KeyedItems: map[string]*testpb.LineItem{"k": {Sku: "b"}},
				Insured:    true, Fragile: proto.Bool(false), Labels: []string{"x"},
			}
			Expect(aip.ImpliedUpdateMask(shipment).GetPaths()).To(Equal(
				aip.MutablePaths(shipment.ProtoReflect().Descriptor())))
		})

		It("returns nil for a nil descriptor", func() {
			Expect(aip.MutablePaths(nil)).To(BeNil())
		})
	})

	Describe("ImpliedUpdateMask", func() {
		It("names every populated writable field, in declaration order", func() {
			shipment := &testpb.Shipment{
				Origin:     "Berlin",
				Notes:      "fragile goods",
				Carrier:    &testpb.Carrier{Name: "DHL"},
				LineItems:  []*testpb.LineItem{{Sku: "a"}},
				KeyedItems: map[string]*testpb.LineItem{"k": {Sku: "b"}},
				Insured:    true,
				Labels:     []string{"express"},
			}
			Expect(aip.ImpliedUpdateMask(shipment).GetPaths()).To(Equal([]string{
				"origin", "notes", "carrier", "line_items", "keyed_items", "insured", "labels",
			}))
		})

		It("leaves out OUTPUT_ONLY, IDENTIFIER and IMMUTABLE fields even when set", func() {
			shipment := &testpb.Shipment{
				Name:        "shipments/1",     // IDENTIFIER
				CreateTime:  timestamppb.Now(), // OUTPUT_ONLY
				CarrierCode: "DHL",             // IMMUTABLE
				Notes:       "the only writable one",
			}
			Expect(aip.ImpliedUpdateMask(shipment).GetPaths()).To(Equal([]string{"notes"}))
		})

		It("leaves out unset fields", func() {
			// An implicit-presence scalar at its zero value is unset; so is an
			// empty repeated field or map, and a nil message.
			shipment := &testpb.Shipment{
				Insured:    false,
				Labels:     []string{},
				KeyedItems: map[string]*testpb.LineItem{},
			}
			Expect(aip.ImpliedUpdateMask(shipment).GetPaths()).To(BeEmpty())
		})

		It("includes an optional field set to its zero value", func() {
			shipment := &testpb.Shipment{Fragile: proto.Bool(false)}
			Expect(aip.ImpliedUpdateMask(shipment).GetPaths()).To(Equal([]string{"fragile"}))
		})

		It("returns an empty, non-nil mask for a nil resource", func() {
			mask := aip.ImpliedUpdateMask(nil)
			Expect(mask).NotTo(BeNil())
			Expect(mask.GetPaths()).To(BeEmpty())

			var shipment *testpb.Shipment
			mask = aip.ImpliedUpdateMask(shipment)
			Expect(mask).NotTo(BeNil())
			Expect(mask.GetPaths()).To(BeEmpty())
		})

		It("implies a populated message as one path, so OUTPUT_ONLY beneath it is cleared first", func() {
			// carrier is implied whole; its tracking_id is OUTPUT_ONLY and would
			// be written with it. Clear, then imply.
			shipment := &testpb.Shipment{Carrier: &testpb.Carrier{Name: "DHL", TrackingId: "t1"}}
			aip.ClearFields(shipment, aip.OutputOnly)
			Expect(aip.ImpliedUpdateMask(shipment).GetPaths()).To(Equal([]string{"carrier"}))
			Expect(shipment.GetCarrier().GetTrackingId()).To(BeEmpty())
			Expect(shipment.GetCarrier().GetName()).To(Equal("DHL"))
		})

		It("does not modify the resource", func() {
			shipment := &testpb.Shipment{Name: "shipments/1", Notes: "n"}
			before := proto.Clone(shipment)
			aip.ImpliedUpdateMask(shipment)
			Expect(proto.Equal(shipment, before)).To(BeTrue())
		})
	})
})
