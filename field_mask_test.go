package aip_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/protoc-contrib/aip-go"
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
})
