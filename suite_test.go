package aip_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAIP(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "AIP Suite")
}
