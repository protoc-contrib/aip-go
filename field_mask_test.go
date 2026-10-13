package aip_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/protoc-contrib/aip-go"
	testpb "github.com/protoc-contrib/aip-go/internal/testpb"
)

var _ = Describe("FieldMask", func() {
	Describe("ImpliedFieldMask", func() {
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
			Expect(aip.ImpliedFieldMask(shipment).GetPaths()).To(Equal([]string{
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
			Expect(aip.ImpliedFieldMask(shipment).GetPaths()).To(Equal([]string{"notes"}))
		})

		It("names exactly the writable fields when every field is set", func() {
			shipment := &testpb.Shipment{
				Name: "shipments/1", Origin: "Berlin", Destination: "Paris",
				CreateTime: timestamppb.Now(), Notes: "n", CarrierCode: "DHL",
				Carrier:    &testpb.Carrier{Name: "DHL"},
				LineItems:  []*testpb.LineItem{{Sku: "a"}},
				KeyedItems: map[string]*testpb.LineItem{"k": {Sku: "b"}},
				Insured:    true, Fragile: proto.Bool(false), Labels: []string{"x"},
				OriginCarrier: &testpb.Carrier{Name: "UPS"}, Route: []string{"a"},
				Stops: []string{"b"}, Escorts: []*testpb.Carrier{{Name: "c"}},
			}
			Expect(aip.ImpliedFieldMask(shipment).GetPaths()).To(Equal([]string{
				"origin", "destination", "notes", "carrier", "line_items",
				"keyed_items", "insured", "fragile", "labels",
			}))
		})

		It("leaves out unset fields", func() {
			// An implicit-presence scalar at its zero value is unset; so is an
			// empty repeated field or map, and a nil message.
			shipment := &testpb.Shipment{
				Insured:    false,
				Labels:     []string{},
				KeyedItems: map[string]*testpb.LineItem{},
			}
			Expect(aip.ImpliedFieldMask(shipment).GetPaths()).To(BeEmpty())
		})

		It("includes an optional field set to its zero value", func() {
			shipment := &testpb.Shipment{Fragile: proto.Bool(false)}
			Expect(aip.ImpliedFieldMask(shipment).GetPaths()).To(Equal([]string{"fragile"}))
		})

		It("returns an empty, non-nil mask for a nil message", func() {
			mask := aip.ImpliedFieldMask(nil)
			Expect(mask).NotTo(BeNil())
			Expect(mask.GetPaths()).To(BeEmpty())

			var shipment *testpb.Shipment
			mask = aip.ImpliedFieldMask(shipment)
			Expect(mask).NotTo(BeNil())
			Expect(mask.GetPaths()).To(BeEmpty())
		})

		It("implies a populated message as one path, so OUTPUT_ONLY beneath it is cleared first", func() {
			// carrier is implied whole; its tracking_id is OUTPUT_ONLY and would
			// be written with it. Clear, then imply.
			shipment := &testpb.Shipment{Carrier: &testpb.Carrier{Name: "DHL", TrackingId: "t1"}}
			aip.ClearFields(shipment, aip.OutputOnly)
			Expect(aip.ImpliedFieldMask(shipment).GetPaths()).To(Equal([]string{"carrier"}))
			Expect(shipment.GetCarrier().GetTrackingId()).To(BeEmpty())
			Expect(shipment.GetCarrier().GetName()).To(Equal("DHL"))
		})

		It("does not modify the message", func() {
			shipment := &testpb.Shipment{Name: "shipments/1", Notes: "n"}
			before := proto.Clone(shipment)
			aip.ImpliedFieldMask(shipment)
			Expect(proto.Equal(shipment, before)).To(BeTrue())
		})
	})
})
