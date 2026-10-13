package aip_test

import (
	"errors"
	"fmt"
	"strings"

	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/reflect/protopath"

	"github.com/protoc-contrib/aip-go"
)

// unsetFields returns the fields a *aip.FieldBehaviorError names, dotted and
// without the root step, or nil for a nil error.
func unsetFields(err error) []string {
	if err == nil {
		return nil
	}
	var fields *aip.FieldBehaviorError
	Expect(errors.As(err, &fields)).To(BeTrue(), "%v is not a *aip.FieldBehaviorError", err)
	Expect(fields.Paths).NotTo(BeEmpty())
	return dotted(fields.Paths)
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
