package aip_test

import (
	"errors"
	"fmt"
	"strings"

	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/reflect/protopath"

	"github.com/protoc-contrib/aip-go"
)

// requiredPaths returns the paths a *aip.RequiredError holds, or nil for a nil
// error.
func requiredPaths(err error) []protopath.Path {
	if err == nil {
		return nil
	}
	var required *aip.RequiredError
	Expect(errors.As(err, &required)).To(BeTrue(), "%v is not a *aip.RequiredError", err)
	Expect(required.Paths).NotTo(BeEmpty())
	return required.Paths
}

// unsetFields returns the fields a *aip.RequiredError names, dotted and
// without the root step, or nil for a nil error.
func unsetFields(err error) []string {
	return dotted(requiredPaths(err))
}

// changedFields returns the fields an *aip.ImmutableError names, dotted and
// without the root step, or nil for a nil error.
func changedFields(err error) []string {
	if err == nil {
		return nil
	}
	var immutable *aip.ImmutableError
	Expect(errors.As(err, &immutable)).To(BeTrue(), "%v is not an *aip.ImmutableError", err)
	Expect(immutable.Paths).NotTo(BeEmpty())
	return dotted(immutable.Paths)
}

// dotted renders paths as field names joined by dots, list indices in
// brackets, without the root step.
func dotted(paths []protopath.Path) []string {
	if paths == nil {
		return nil
	}
	rendered := make([]string, 0, len(paths))
	for _, path := range paths {
		var b strings.Builder
		for _, step := range path {
			switch step.Kind() {
			case protopath.FieldAccessStep:
				if b.Len() > 0 {
					b.WriteByte('.')
				}
				b.WriteString(string(step.FieldDescriptor().Name()))
			case protopath.ListIndexStep:
				fmt.Fprintf(&b, "[%d]", step.ListIndex())
			}
		}
		rendered = append(rendered, b.String())
	}
	return rendered
}
