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

	Describe("ValidateImmutable", func() {
		stored := func() *testpb.Shipment {
			shipment := validShipment()
			shipment.CarrierCode = "DHL"
			shipment.OriginCarrier = &testpb.Carrier{Name: "UPS"}
			shipment.Route = []string{"BER", "CDG"}
			return shipment
		}

		It("reports nothing when every immutable value is echoed back", func() {
			Expect(changedFields(aip.ValidateImmutable(stored(), stored()))).To(BeEmpty())
		})

		It("reports each changed immutable field, in declaration order", func() {
			update := stored()
			update.Route = []string{"BER", "AMS"}
			update.CarrierCode = "FedEx"
			Expect(changedFields(aip.ValidateImmutable(stored(), update))).To(Equal([]string{"carrier_code", "route"}))
		})

		It("ignores an immutable field the update leaves unset", func() {
			update := &testpb.Shipment{Notes: "only this"}
			Expect(changedFields(aip.ValidateImmutable(stored(), update))).To(BeEmpty())
		})

		It("compares a message-typed immutable field by content", func() {
			update := stored()
			update.OriginCarrier = &testpb.Carrier{Name: "UPS"}
			Expect(changedFields(aip.ValidateImmutable(stored(), update))).To(BeEmpty())

			update.OriginCarrier = &testpb.Carrier{Name: "UPS", TrackingId: "T1"}
			Expect(changedFields(aip.ValidateImmutable(stored(), update))).To(Equal([]string{"origin_carrier"}))
		})

		It("reports setting an immutable field the stored resource never had", func() {
			existing := validShipment()
			update := validShipment()
			update.CarrierCode = "DHL"
			Expect(changedFields(aip.ValidateImmutable(existing, update))).To(Equal([]string{"carrier_code"}))
		})

		It("ignores mutable and output-only fields however they change", func() {
			update := stored()
			update.Notes = "different"
			update.CreateTime = timestamppb.New(time.Unix(1700000000, 0))
			Expect(changedFields(aip.ValidateImmutable(stored(), update))).To(BeEmpty())
		})

		It("returns nil for a nil or typed-nil message", func() {
			Expect(changedFields(aip.ValidateImmutable(nil, stored()))).To(BeNil())
			Expect(changedFields(aip.ValidateImmutable(stored(), nil))).To(BeNil())
			Expect(changedFields(aip.ValidateImmutable((*testpb.Shipment)(nil), stored()))).To(BeNil())
		})

		It("panics on two different message types", func() {
			Expect(func() { changedFields(aip.ValidateImmutable(stored(), &testpb.Carrier{})) }).To(Panic())
		})

		It("modifies neither message", func() {
			existing, update := stored(), stored()
			update.CarrierCode = "FedEx"
			before, beforeUpdate := proto.Clone(existing), proto.Clone(update)
			_ = aip.ValidateImmutable(existing, update)
			Expect(proto.Equal(existing, before)).To(BeTrue())
			Expect(proto.Equal(update, beforeUpdate)).To(BeTrue())
		})

		It("reports an ordered immutable list echoed back in another order", func() {
			update := stored()
			update.Route = []string{"CDG", "BER"}
			Expect(changedFields(aip.ValidateImmutable(stored(), update))).To(Equal([]string{"route"}))
		})

		Context("with an UNORDERED_LIST field", func() {
			unordered := func() *testpb.Shipment {
				shipment := stored()
				shipment.Stops = []string{"BER", "CDG", "BER"}
				shipment.Escorts = []*testpb.Carrier{{Name: "UPS"}, {Name: "DHL"}}
				return shipment
			}

			It("reports nothing when the list comes back in another order", func() {
				update := unordered()
				update.Stops = []string{"CDG", "BER", "BER"}
				update.Escorts = []*testpb.Carrier{{Name: "DHL"}, {Name: "UPS"}}
				Expect(changedFields(aip.ValidateImmutable(unordered(), update))).To(BeEmpty())
			})

			It("counts repeated elements", func() {
				update := unordered()
				update.Stops = []string{"BER", "CDG", "CDG"}
				Expect(changedFields(aip.ValidateImmutable(unordered(), update))).To(Equal([]string{"stops"}))
			})

			It("reports a different element or length", func() {
				update := unordered()
				update.Stops = []string{"BER", "CDG"}
				update.Escorts = []*testpb.Carrier{{Name: "UPS"}, {Name: "FedEx"}}
				Expect(changedFields(aip.ValidateImmutable(unordered(), update))).To(Equal([]string{"stops", "escorts"}))
			})
		})
	})

	Describe("ValidateRequiredFields", func() {
		mask := func(paths ...string) *fieldmaskpb.FieldMask {
			return &fieldmaskpb.FieldMask{Paths: paths}
		}

		Context("on create, with a nil mask", func() {
			It("reports nothing when every required field is set", func() {
				Expect(unsetFields(aip.ValidateRequiredFields(validShipment(), nil))).To(BeNil())
			})

			It("reports every unset required field, in declaration order", func() {
				Expect(unsetFields(aip.ValidateRequiredFields(&testpb.Shipment{}, nil))).To(Equal([]string{
					"origin", "destination", "insured", "fragile", "labels",
				}))
			})

			It("checks a nested message that is set", func() {
				shipment := validShipment()
				shipment.Carrier = &testpb.Carrier{TrackingId: "XYZ"}
				Expect(unsetFields(aip.ValidateRequiredFields(shipment, nil))).To(Equal([]string{"carrier.name"}))
			})

			It("skips a nested message that is unset", func() {
				shipment := validShipment()
				shipment.Carrier = nil
				Expect(unsetFields(aip.ValidateRequiredFields(shipment, nil))).To(BeNil())
			})

			It("reports a nested field depth first, before later siblings", func() {
				shipment := validShipment()
				shipment.Carrier = &testpb.Carrier{}
				shipment.Labels = nil
				Expect(unsetFields(aip.ValidateRequiredFields(shipment, nil))).To(Equal([]string{"carrier.name", "labels"}))
			})
		})

		Context("on update, with a mask", func() {
			It("reports a named required field that is unset", func() {
				shipment := validShipment()
				shipment.Origin = ""
				Expect(unsetFields(aip.ValidateRequiredFields(shipment, mask("origin")))).To(Equal([]string{"origin"}))
			})

			It("ignores a required field the mask does not name", func() {
				Expect(unsetFields(aip.ValidateRequiredFields(&testpb.Shipment{}, mask("notes")))).To(BeNil())
			})

			It("reports nothing for an empty mask", func() {
				Expect(unsetFields(aip.ValidateRequiredFields(&testpb.Shipment{}, mask()))).To(BeNil())
			})

			It("checks everything beneath a named message field", func() {
				shipment := &testpb.Shipment{Carrier: &testpb.Carrier{TrackingId: "XYZ"}}
				Expect(unsetFields(aip.ValidateRequiredFields(shipment, mask("carrier")))).To(Equal([]string{"carrier.name"}))
			})

			It("checks only the nested field a nested path names", func() {
				shipment := &testpb.Shipment{Carrier: &testpb.Carrier{}}
				Expect(unsetFields(aip.ValidateRequiredFields(shipment, mask("carrier.name")))).To(Equal([]string{"carrier.name"}))
				Expect(unsetFields(aip.ValidateRequiredFields(shipment, mask("carrier.tracking_id")))).To(BeNil())
			})

			It("checks a nested path even when the request leaves its message unset", func() {
				Expect(unsetFields(aip.ValidateRequiredFields(&testpb.Shipment{}, mask("carrier.name")))).To(Equal([]string{"carrier.name"}))
			})

			It("reports a path once when the mask names it twice", func() {
				shipment := &testpb.Shipment{Carrier: &testpb.Carrier{}}
				Expect(unsetFields(aip.ValidateRequiredFields(shipment, mask("carrier", "carrier.name")))).To(Equal([]string{"carrier.name"}))
			})

			It("matches whole segments, not string prefixes", func() {
				Expect(unsetFields(aip.ValidateRequiredFields(&testpb.Shipment{}, mask("orig")))).To(BeNil())
			})

			It("does not expand the wildcard", func() {
				Expect(unsetFields(aip.ValidateRequiredFields(&testpb.Shipment{}, mask("*")))).To(BeNil())
			})
		})

		Context("reading presence", func() {
			It("treats an explicit false on an optional field as set", func() {
				shipment := validShipment()
				shipment.Fragile = proto.Bool(false)
				Expect(unsetFields(aip.ValidateRequiredFields(shipment, nil))).To(BeNil())
			})

			It("treats a presence-less zero value as unset", func() {
				shipment := validShipment()
				shipment.Origin = ""
				Expect(unsetFields(aip.ValidateRequiredFields(shipment, nil))).To(Equal([]string{"origin"}))
			})

			It("requires a presence-less required bool to be true", func() {
				shipment := validShipment()
				shipment.Insured = false
				Expect(unsetFields(aip.ValidateRequiredFields(shipment, nil))).To(Equal([]string{"insured"}))
			})
		})

		Context("with repeated and map fields", func() {
			It("checks a required repeated field itself", func() {
				shipment := validShipment()
				shipment.Labels = nil
				Expect(unsetFields(aip.ValidateRequiredFields(shipment, nil))).To(Equal([]string{"labels"}))
			})

			It("does not descend into repeated messages or map values", func() {
				shipment := validShipment()
				shipment.LineItems = []*testpb.LineItem{{Quantity: 2}}
				shipment.KeyedItems = map[string]*testpb.LineItem{"x": {Quantity: 1}}
				Expect(unsetFields(aip.ValidateRequiredFields(shipment, nil))).To(BeNil())
				Expect(unsetFields(aip.ValidateRequiredFields(shipment, mask("line_items.sku", "keyed_items.sku")))).To(BeNil())
			})
		})

		It("returns nil for a nil or typed-nil message", func() {
			Expect(unsetFields(aip.ValidateRequiredFields(nil, nil))).To(BeNil())
			Expect(unsetFields(aip.ValidateRequiredFields((*testpb.Shipment)(nil), nil))).To(BeNil())
		})

		It("modifies neither the message nor the mask", func() {
			shipment := &testpb.Shipment{Carrier: &testpb.Carrier{}}
			paths := mask("carrier.name", "origin")
			before, beforeMask := proto.Clone(shipment), proto.Clone(paths)
			_ = aip.ValidateRequiredFields(shipment, paths)
			Expect(proto.Equal(shipment, before)).To(BeTrue())
			Expect(proto.Equal(paths, beforeMask)).To(BeTrue())
		})
	})
})
