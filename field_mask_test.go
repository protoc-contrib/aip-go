package aip_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/protoc-contrib/aip-go"
	testpb "github.com/protoc-contrib/aip-go/internal/testpb"
)

var _ = Describe("FieldMask", func() {
	Describe("ValidateFieldMask", func() {
		DescribeTable("accepts applicable masks",
			func(paths ...string) {
				mask := &fieldmaskpb.FieldMask{Paths: paths}
				Expect(aip.ValidateFieldMask(mask, &testpb.Shipment{})).To(Succeed())
			},
			Entry("single field", "origin"),
			Entry("several fields", "origin", "destination", "notes"),
			Entry("nested field", "carrier.name"),
			Entry("well-known message", "create_time"),
			Entry("repeated field as a leaf", "line_items"),
			Entry("map field as a leaf", "keyed_items"),
			Entry("wildcard alone", "*"),
		)

		It("accepts a nil mask", func() {
			Expect(aip.ValidateFieldMask(nil, &testpb.Shipment{})).To(Succeed())
		})

		It("accepts an empty mask", func() {
			Expect(aip.ValidateFieldMask(&fieldmaskpb.FieldMask{}, &testpb.Shipment{})).To(Succeed())
		})

		DescribeTable("rejects inapplicable masks",
			func(message string, paths ...string) {
				mask := &fieldmaskpb.FieldMask{Paths: paths}

				err := aip.ValidateFieldMask(mask, &testpb.Shipment{})
				Expect(err).To(MatchError(aip.ErrInvalidFieldMask))
				Expect(err).To(MatchError(ContainSubstring(message)))
			},
			Entry("unknown field", `no field "nope"`, "nope"),
			Entry("unknown nested field", `no field "nope"`, "carrier.nope"),
			Entry("through a scalar", "cannot traverse into scalar", "origin.length"),
			Entry("through a repeated field", "cannot traverse into repeated", "line_items.sku"),
			Entry("through a map field", "cannot traverse into map", "keyed_items.sku"),
			Entry("empty path", "empty path", ""),
			Entry("wildcard with another path", "must not be combined", "*", "origin"),
			Entry("another path with wildcard", "must not be combined", "origin", "*"),
		)

		It("reports the first inapplicable path", func() {
			mask := &fieldmaskpb.FieldMask{Paths: []string{"origin", "nope"}}
			Expect(aip.ValidateFieldMask(mask, &testpb.Shipment{})).To(MatchError(ContainSubstring("nope")))
		})
	})

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
})
