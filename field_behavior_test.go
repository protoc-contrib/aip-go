package aip_test

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protopath"
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
	Describe("FieldBehaviorsOf", func() {
		fields := (&testpb.Shipment{}).ProtoReflect().Descriptor().Fields()

		It("reads the annotated behaviors as a set", func() {
			origin := aip.FieldBehaviorsOf(fields.ByName("origin"))
			Expect(origin.Has(aip.Required)).To(BeTrue())
			Expect(origin.Has(aip.OutputOnly)).To(BeFalse())
			Expect(aip.FieldBehaviorsOf(fields.ByName("create_time")).Has(aip.OutputOnly)).To(BeTrue())
			Expect(aip.FieldBehaviorsOf(fields.ByName("name")).Has(aip.Identifier)).To(BeTrue())
		})

		It("holds every behavior of a field annotated with several", func() {
			stops := aip.FieldBehaviorsOf(fields.ByName("stops"))
			Expect(stops.Has(aip.Immutable)).To(BeTrue())
			Expect(stops.Has(aip.UnorderedList)).To(BeTrue())
			Expect(slices.Collect(stops.All())).To(Equal([]aip.FieldBehaviorKind{aip.Immutable, aip.UnorderedList}))
			Expect(stops.String()).To(Equal("[IMMUTABLE UNORDERED_LIST]"))
		})

		It("returns the empty set for an unannotated field", func() {
			notes := aip.FieldBehaviorsOf(fields.ByName("notes"))
			Expect(notes).To(Equal(aip.FieldBehaviorSet(0)))
			Expect(notes.Has(aip.Required)).To(BeFalse())
			Expect(notes.HasAny(aip.Required, aip.OutputOnly)).To(BeFalse())
			Expect(slices.Collect(notes.All())).To(BeEmpty())
			Expect(notes.String()).To(Equal("[]"))
		})

		It("returns an equal set on every call, from its cache", func() {
			field := fields.ByName("escorts")
			Expect(aip.FieldBehaviorsOf(field)).To(Equal(aip.FieldBehaviorsOf(field)))
		})

		It("is safe for concurrent use", func() {
			field := fields.ByName("carrier_code")
			var wg sync.WaitGroup
			results := make([]aip.FieldBehaviorSet, 32)
			for i := range results {
				wg.Add(1)
				go func() {
					defer wg.Done()
					results[i] = aip.FieldBehaviorsOf(field)
				}()
			}
			wg.Wait()
			for _, result := range results {
				Expect(result.Has(aip.Immutable)).To(BeTrue())
			}
		})
	})

	Describe("FieldBehaviorSet", func() {
		set := aip.FieldBehaviorsOf((&testpb.Shipment{}).ProtoReflect().Descriptor().Fields().ByName("stops"))

		It("reports whether it holds any of several kinds", func() {
			Expect(set.HasAny(aip.OutputOnly, aip.Immutable)).To(BeTrue())
			Expect(set.HasAny(aip.OutputOnly, aip.Identifier)).To(BeFalse())
			Expect(set.HasAny()).To(BeFalse())
		})

		It("yields its kinds in enum order and stops when asked", func() {
			var first []aip.FieldBehaviorKind
			for kind := range set.All() {
				first = append(first, kind)
				break
			}
			Expect(first).To(Equal([]aip.FieldBehaviorKind{aip.Immutable}))
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

		It("clears only the given kind", func() {
			shipment := validShipment()
			shipment.CreateTime = timestamppb.New(time.Unix(1700000000, 0))
			shipment.CarrierCode = "DHL"

			aip.ClearFields(shipment, aip.OutputOnly)
			Expect(shipment.GetCreateTime()).To(BeNil())
			Expect(shipment.GetCarrierCode()).To(Equal("DHL"))

			aip.ClearFields(shipment, aip.Immutable)
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

		It("leaves a nil or typed-nil message alone", func() {
			Expect(func() { aip.ClearFields(nil, aip.OutputOnly) }).NotTo(Panic())
			Expect(func() { aip.ClearFields((*testpb.Shipment)(nil), aip.OutputOnly) }).NotTo(Panic())
		})
	})

	Describe("ValidateFields", func() {
		mask := func(paths ...string) *fieldmaskpb.FieldMask {
			return &fieldmaskpb.FieldMask{Paths: paths}
		}

		Context("on create, with a nil mask", func() {
			It("reports nothing when every required field is set", func() {
				Expect(unsetFields(aip.ValidateFields(validShipment(), aip.Required, nil))).To(BeNil())
			})

			It("reports every unset required field, in declaration order", func() {
				Expect(unsetFields(aip.ValidateFields(&testpb.Shipment{}, aip.Required, nil))).To(Equal([]string{
					"origin", "destination", "insured", "fragile", "labels",
				}))
			})

			It("checks a nested message that is set", func() {
				shipment := validShipment()
				shipment.Carrier = &testpb.Carrier{TrackingId: "XYZ"}
				Expect(unsetFields(aip.ValidateFields(shipment, aip.Required, nil))).To(Equal([]string{"carrier.name"}))
			})

			It("skips a nested message that is unset", func() {
				shipment := validShipment()
				shipment.Carrier = nil
				Expect(unsetFields(aip.ValidateFields(shipment, aip.Required, nil))).To(BeNil())
			})

			It("reports a nested field depth first, before later siblings", func() {
				shipment := validShipment()
				shipment.Carrier = &testpb.Carrier{}
				shipment.Labels = nil
				Expect(unsetFields(aip.ValidateFields(shipment, aip.Required, nil))).To(Equal([]string{"carrier.name", "labels"}))
			})
		})

		Context("on update, with a mask", func() {
			It("reports a named required field that is unset", func() {
				shipment := validShipment()
				shipment.Origin = ""
				Expect(unsetFields(aip.ValidateFields(shipment, aip.Required, mask("origin")))).To(Equal([]string{"origin"}))
			})

			It("ignores a required field the mask does not name", func() {
				Expect(unsetFields(aip.ValidateFields(&testpb.Shipment{}, aip.Required, mask("notes")))).To(BeNil())
			})

			It("reports nothing for an empty mask", func() {
				Expect(unsetFields(aip.ValidateFields(&testpb.Shipment{}, aip.Required, mask()))).To(BeNil())
			})

			It("checks everything beneath a named message field", func() {
				shipment := &testpb.Shipment{Carrier: &testpb.Carrier{TrackingId: "XYZ"}}
				Expect(unsetFields(aip.ValidateFields(shipment, aip.Required, mask("carrier")))).To(Equal([]string{"carrier.name"}))
			})

			It("checks only the nested field a nested path names", func() {
				shipment := &testpb.Shipment{Carrier: &testpb.Carrier{}}
				Expect(unsetFields(aip.ValidateFields(shipment, aip.Required, mask("carrier.name")))).To(Equal([]string{"carrier.name"}))
				Expect(unsetFields(aip.ValidateFields(shipment, aip.Required, mask("carrier.tracking_id")))).To(BeNil())
			})

			It("checks a nested path even when the request leaves its message unset", func() {
				Expect(unsetFields(aip.ValidateFields(&testpb.Shipment{}, aip.Required, mask("carrier.name")))).To(Equal([]string{"carrier.name"}))
			})

			It("reports a path once when the mask names it twice", func() {
				shipment := &testpb.Shipment{Carrier: &testpb.Carrier{}}
				Expect(unsetFields(aip.ValidateFields(shipment, aip.Required, mask("carrier", "carrier.name")))).To(Equal([]string{"carrier.name"}))
			})

			It("matches whole segments, not string prefixes", func() {
				Expect(unsetFields(aip.ValidateFields(&testpb.Shipment{}, aip.Required, mask("orig")))).To(BeNil())
			})

			It("does not expand the wildcard", func() {
				Expect(unsetFields(aip.ValidateFields(&testpb.Shipment{}, aip.Required, mask("*")))).To(BeNil())
			})
		})

		Context("reading presence", func() {
			It("treats an explicit false on an optional field as set", func() {
				shipment := validShipment()
				shipment.Fragile = proto.Bool(false)
				Expect(unsetFields(aip.ValidateFields(shipment, aip.Required, nil))).To(BeNil())
			})

			It("treats a presence-less zero value as unset", func() {
				shipment := validShipment()
				shipment.Origin = ""
				Expect(unsetFields(aip.ValidateFields(shipment, aip.Required, nil))).To(Equal([]string{"origin"}))
			})

			It("requires a presence-less required bool to be true", func() {
				shipment := validShipment()
				shipment.Insured = false
				Expect(unsetFields(aip.ValidateFields(shipment, aip.Required, nil))).To(Equal([]string{"insured"}))
			})
		})

		Context("with repeated and map fields", func() {
			It("checks a required repeated field itself", func() {
				shipment := validShipment()
				shipment.Labels = nil
				Expect(unsetFields(aip.ValidateFields(shipment, aip.Required, nil))).To(Equal([]string{"labels"}))
			})

			It("does not descend into repeated messages or map values", func() {
				shipment := validShipment()
				shipment.LineItems = []*testpb.LineItem{{Quantity: 2}}
				shipment.KeyedItems = map[string]*testpb.LineItem{"x": {Quantity: 1}}
				Expect(unsetFields(aip.ValidateFields(shipment, aip.Required, nil))).To(BeNil())
				Expect(unsetFields(aip.ValidateFields(shipment, aip.Required, mask("line_items.sku", "keyed_items.sku")))).To(BeNil())
			})
		})

		Context("with another kind", func() {
			It("checks the fields annotated with that kind", func() {
				Expect(unsetFields(aip.ValidateFields(validShipment(), aip.Immutable, nil))).To(Equal([]string{
					"carrier_code", "origin_carrier", "route", "stops", "escorts",
				}))
			})

			It("scopes them by the mask as for REQUIRED", func() {
				Expect(unsetFields(aip.ValidateFields(validShipment(), aip.Immutable, mask("carrier_code")))).To(Equal([]string{"carrier_code"}))
			})

			It("reports the kind it checked", func() {
				var fields *aip.FieldBehaviorError
				Expect(errors.As(aip.ValidateFields(validShipment(), aip.Immutable, nil), &fields)).To(BeTrue())
				Expect(fields.Kind).To(Equal(aip.Immutable))
			})
		})

		It("names the kind and the dotted paths in its message", func() {
			shipment := validShipment()
			shipment.Origin = ""
			shipment.Carrier = &testpb.Carrier{}
			Expect(aip.ValidateFields(shipment, aip.Required, nil)).To(MatchError("REQUIRED fields are unset: origin, carrier.name"))
		})

		It("returns nil for a nil or typed-nil message", func() {
			Expect(unsetFields(aip.ValidateFields(nil, aip.Required, nil))).To(BeNil())
			Expect(unsetFields(aip.ValidateFields((*testpb.Shipment)(nil), aip.Required, nil))).To(BeNil())
		})

		It("modifies neither the message nor the mask", func() {
			shipment := &testpb.Shipment{Carrier: &testpb.Carrier{}}
			paths := mask("carrier.name", "origin")
			before, beforeMask := proto.Clone(shipment), proto.Clone(paths)
			_ = aip.ValidateFields(shipment, aip.Required, paths)
			Expect(proto.Equal(shipment, before)).To(BeTrue())
			Expect(proto.Equal(paths, beforeMask)).To(BeTrue())
		})
	})
})

var _ = Describe("FieldError", func() {
	shipment := &testpb.Shipment{}

	Describe("FieldPath", func() {
		It("starts at the message and names each field it walks through", func() {
			path := aip.FieldPath(shipment, "carrier.name")
			Expect(path.String()).To(Equal("(tests.Shipment).carrier.name"))
			Expect(path[0].Kind()).To(Equal(protopath.RootStep))
			Expect(path).To(HaveLen(3))
		})

		It("takes a typed-nil message", func() {
			Expect(aip.FieldPath((*testpb.Shipment)(nil), "origin").String()).To(Equal("(tests.Shipment).origin"))
		})

		It("descends into a repeated message field's element type", func() {
			Expect(aip.FieldPath(shipment, "line_items.sku").String()).To(Equal("(tests.Shipment).line_items.sku"))
		})

		It("panics, naming the message and the field, for a name that is no field", func() {
			Expect(func() { aip.FieldPath(shipment, "colection") }).To(PanicWith(
				`aip.FieldPath: tests.Shipment has no field "colection"`,
			))
			Expect(func() { aip.FieldPath(shipment, "carrier.nme") }).To(PanicWith(
				`aip.FieldPath: tests.Carrier has no field "nme"`,
			))
		})

		It("panics for a name that walks through a field that holds no message", func() {
			Expect(func() { aip.FieldPath(shipment, "origin.name") }).To(PanicWith(
				`aip.FieldPath: "origin" is not a message, so "name" is no field of it`,
			))
		})
	})

	Describe("Field", func() {
		It("renders the path without its root", func() {
			Expect(aip.NewFieldError(shipment, "origin", errors.New("x")).Field()).To(Equal("origin"))
			Expect(aip.NewFieldError(shipment, "carrier.name", errors.New("x")).Field()).To(Equal("carrier.name"))
		})

		It("renders a list index in brackets", func() {
			lineItems := shipment.ProtoReflect().Descriptor().Fields().ByName("line_items")
			sku := lineItems.Message().Fields().ByName("sku")
			path := protopath.Path{
				protopath.Root(shipment.ProtoReflect().Descriptor()),
				protopath.FieldAccess(lineItems),
				protopath.ListIndex(1),
				protopath.FieldAccess(sku),
			}
			Expect((&aip.FieldError{Path: path}).Field()).To(Equal("line_items[1].sku"))
		})
	})

	Describe("Error", func() {
		It("names the field and why", func() {
			err := aip.NewFieldError(shipment, "carrier.name", errors.New(`parse "x": bad segment`))
			Expect(err).To(MatchError(`carrier.name: parse "x": bad segment`))
		})

		It("names the field alone when there is no cause", func() {
			Expect(aip.NewFieldError(shipment, "origin", nil)).To(MatchError("origin"))
		})
	})

	It("is found through a wrapped chain, and unwraps to its cause", func() {
		cause := errors.New("bad segment")
		err := fmt.Errorf("handler: %w", aip.NewFieldError(shipment, "origin", cause))

		var field *aip.FieldError
		Expect(errors.As(err, &field)).To(BeTrue())
		Expect(field.Field()).To(Equal("origin"))
		Expect(field.Unwrap()).To(BeIdenticalTo(cause))
		Expect(errors.Is(err, cause)).To(BeTrue())
	})
})
