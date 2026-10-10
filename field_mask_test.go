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
			Entry("nil mask", nil, true),
			Entry("empty mask", &fieldmaskpb.FieldMask{}, true),
			Entry("wildcard", &fieldmaskpb.FieldMask{Paths: []string{"*"}}, true),
			Entry("single field", &fieldmaskpb.FieldMask{Paths: []string{"origin"}}, false),
			Entry("wildcard with another path", &fieldmaskpb.FieldMask{Paths: []string{"*", "origin"}}, false),
		)
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

		It("does not modify the resource", func() {
			shipment := &testpb.Shipment{Name: "shipments/1", Notes: "n"}
			before := proto.Clone(shipment)
			aip.ImpliedUpdateMask(shipment)
			Expect(proto.Equal(shipment, before)).To(BeTrue())
		})
	})
})
