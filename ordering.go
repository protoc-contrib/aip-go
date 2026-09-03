package aip

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/protoc-contrib/aip-go/internal/protopath"
	"google.golang.org/protobuf/proto"
)

// OrderByRequest is a List request that supports ordering.
//
// See: https://google.aip.dev/132#ordering.
type OrderByRequest interface {
	// GetOrderBy returns the `order_by` value of the request.
	GetOrderBy() string
}

// OrderBy is a parsed AIP-132 `order_by` value.
type OrderBy struct {
	// Fields are the ordering fields, in significance order.
	Fields []OrderByField
}

// OrderByField is a single ordering term.
type OrderByField struct {
	// Path is the field path, dotted for nested fields (`author.name`).
	Path string
	// Desc reports whether the field orders descending.
	Desc bool
}

// ParseOrderBy parses the `order_by` value on request.
func ParseOrderBy(request OrderByRequest) (OrderBy, error) {
	var orderBy OrderBy
	if err := orderBy.UnmarshalString(request.GetOrderBy()); err != nil {
		return OrderBy{}, err
	}
	return orderBy, nil
}

// SubFields splits the field path into its segments.
func (f OrderByField) SubFields() []string {
	if f.Path == "" {
		return nil
	}
	return strings.Split(f.Path, ".")
}

// String renders the field back into `order_by` syntax.
func (f OrderByField) String() string {
	if f.Desc {
		return f.Path + " desc"
	}
	return f.Path
}

// Paths returns the ordering field paths, in order.
//
// The result lines up positionally with a key-set cursor, so it is what you
// pass to [PageToken.NextCursor].
func (o OrderBy) Paths() []string {
	if len(o.Fields) == 0 {
		return nil
	}
	paths := make([]string, len(o.Fields))
	for i, field := range o.Fields {
		paths[i] = field.Path
	}
	return paths
}

// String renders the ordering back into `order_by` syntax. The result
// re-parses to an equal OrderBy.
func (o OrderBy) String() string {
	terms := make([]string, len(o.Fields))
	for i, field := range o.Fields {
		terms[i] = field.String()
	}
	return strings.Join(terms, ", ")
}

// UnmarshalString parses s into o, replacing any previous content.
//
// The grammar is a comma-separated list of field paths, each optionally
// followed by `asc` or `desc`; an empty string means no ordering.
func (o *OrderBy) UnmarshalString(s string) error {
	o.Fields = nil
	if strings.TrimSpace(s) == "" {
		return nil
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_' && r != ' ' && r != ',' && r != '.' {
			return fmt.Errorf("order_by %q: invalid character %s", s, strconv.QuoteRune(r))
		}
	}
	terms := strings.Split(s, ",")
	fields := make([]OrderByField, 0, len(terms))
	seen := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		parts := strings.Fields(term)
		var field OrderByField
		switch len(parts) {
		case 0:
			// A blank term — a leading, trailing or doubled comma.
			return fmt.Errorf("order_by %q: empty field path", s)
		case 1:
			field = OrderByField{Path: parts[0]}
		case 2:
			switch parts[1] {
			case "asc":
				field = OrderByField{Path: parts[0]}
			case "desc":
				field = OrderByField{Path: parts[0], Desc: true}
			default:
				return fmt.Errorf("order_by %q: expected 'asc' or 'desc', got %q", s, parts[1])
			}
		default:
			return fmt.Errorf("order_by %q: invalid term %q", s, strings.TrimSpace(term))
		}
		if err := validateOrderByPath(field.Path); err != nil {
			return fmt.Errorf("order_by %q: %w", s, err)
		}
		// A repeated path is a client bug: the second occurrence can never
		// affect the result, and under key-set pagination it silently
		// desynchronizes the cursor tuple from the sort key.
		if _, duplicate := seen[field.Path]; duplicate {
			return fmt.Errorf("order_by %q: duplicate field %q", s, field.Path)
		}
		seen[field.Path] = struct{}{}
		fields = append(fields, field)
	}
	o.Fields = fields
	return nil
}

// validateOrderByPath rejects paths that are syntactically malformed,
// independent of any message type.
func validateOrderByPath(path string) error {
	if path == "" {
		return fmt.Errorf("empty field path")
	}
	for _, segment := range strings.Split(path, ".") {
		if segment == "" {
			return fmt.Errorf("field path %q: empty segment", path)
		}
	}
	return nil
}

// ValidateForPaths checks that every ordering field is one of paths.
//
// Use it to enforce the allow-list of orderable fields for a request, so a
// client cannot order by a column you did not intend to expose or index.
func (o OrderBy) ValidateForPaths(paths ...string) error {
	allowed := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		allowed[path] = struct{}{}
	}
	for _, field := range o.Fields {
		if _, ok := allowed[field.Path]; !ok {
			return fmt.Errorf("order_by: field %q is not orderable", field.Path)
		}
	}
	return nil
}

// ValidateForMessage checks that every ordering field resolves to a field on
// message.
//
// This is a schema check, not an authorization check: a field that exists but
// that you do not want clients ordering by still needs
// [OrderBy.ValidateForPaths].
func (o OrderBy) ValidateForMessage(message proto.Message) error {
	descriptor := message.ProtoReflect().Descriptor()
	for _, field := range o.Fields {
		if _, err := protopath.Resolve(descriptor, field.Path); err != nil {
			return fmt.Errorf("order_by: %w", err)
		}
	}
	return nil
}
