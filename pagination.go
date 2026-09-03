package aip

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"

	"github.com/protoc-contrib/aip-go/internal/protopath"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// PageRequest is a paginated List request.
//
// See: https://google.aip.dev/158 (Pagination).
type PageRequest interface {
	proto.Message
	// GetPageToken returns the page token of the request.
	GetPageToken() string
	// GetPageSize returns the page size of the request.
	GetPageSize() int32
}

// skipRequest is a PageRequest that additionally supports skipping results.
//
// See: https://google.aip.dev/158#skipping-results.
type skipRequest interface {
	proto.Message
	// GetSkip returns the number of results to skip.
	GetSkip() int32
}

// PageToken is the opaque state a server hands a client so the client can ask
// for the next page of a List call.
//
// A token supports both AIP-158 pagination styles, and which field is
// meaningful depends on how the page was served:
//
//   - Offset counts rows already returned. Advance it with [PageToken.Next].
//     Simple, but the cost of skipping rows grows with the page number, and
//     concurrent writes shift rows across page boundaries.
//   - Cursor holds the sort-key values of the last row returned. Advance it
//     with [PageToken.NextCursor]. Cost is constant per page and results stay
//     stable under concurrent writes, at the price of requiring an
//     `order_by` whose trailing field is unique.
//
// Use one or the other; a token that carries both is not itself invalid, but
// no query should consult both.
//
// Every token carries a checksum of the request fields that must not change
// between pages, so a client cannot page with one filter and then swap in
// another. Mismatches surface from [ParsePageToken] as [ErrChecksumMismatch].
type PageToken struct {
	// Offset is the number of rows preceding this page.
	Offset int64
	// Cursor is the sort-key tuple of the last row of the previous page.
	Cursor Cursor
	// RequestChecksum is the checksum of the request that produced this token.
	RequestChecksum uint32
}

// pageTokenVersion is the leading byte of every encoded page token.
//
// Bump it whenever the encoding changes in a way that would make an
// already-issued token decode to something different. Tokens written by a
// previous version are then rejected outright rather than misread.
const pageTokenVersion byte = 0x01

// pageTokenChecksumMask is mixed into the request checksum so that a token
// issued by a different token type over the same request does not validate
// here.
const pageTokenChecksumMask uint32 = 0x9acb0442

var (
	// ErrChecksumMismatch is returned by [ParsePageToken] when the token was
	// issued for a materially different request — typically a client that
	// changed `filter` or `order_by` while paging. Map it to
	// InvalidArgument at the RPC boundary.
	ErrChecksumMismatch = errors.New("page token does not match the request")

	// ErrMalformedPageToken is returned when a page token cannot be decoded
	// at all. Map it to InvalidArgument at the RPC boundary.
	ErrMalformedPageToken = errors.New("malformed page token")

	errTruncatedPageToken = fmt.Errorf("%w: truncated", ErrMalformedPageToken)
)

// ParsePageToken decodes and validates the page token on request.
//
// A request with no page token yields the zero-offset token for the first
// page, carrying the current request's checksum — so the result is always
// safe to advance and hand back to the client.
func ParsePageToken(request PageRequest) (PageToken, error) {
	checksum, err := CalculateRequestChecksum(request)
	if err != nil {
		return PageToken{}, err
	}
	checksum ^= pageTokenChecksumMask
	if request.GetPageToken() == "" {
		token := PageToken{RequestChecksum: checksum}
		if skip, ok := request.(skipRequest); ok {
			token.Offset = int64(skip.GetSkip())
		}
		return token, nil
	}
	token, err := DecodePageToken(request.GetPageToken())
	if err != nil {
		return PageToken{}, err
	}
	if token.RequestChecksum != checksum {
		return PageToken{}, fmt.Errorf(
			"%w (token 0x%08x, request 0x%08x)", ErrChecksumMismatch, token.RequestChecksum, checksum,
		)
	}
	if skip, ok := request.(skipRequest); ok {
		token.Offset += int64(skip.GetSkip())
	}
	return token, nil
}

// Next returns the token for the page following this one, under offset
// pagination.
func (t PageToken) Next(request PageRequest) PageToken {
	t.Offset += int64(request.GetPageSize())
	return t
}

// NextCursor returns the token for the page following this one, under key-set
// pagination, by reading paths off message — the last row of the page just
// served.
//
// paths are AIP field paths and may be dotted (`author.name`); they must be
// exactly the ordering fields of the request, in order, so that the cursor
// tuple lines up with the sort key. [OrderBy.Paths] returns them in the right
// shape.
//
// A path that resolves to an unset message or `optional` field contributes a
// nil cursor value, which the query layer should compare as SQL NULL rather
// than as a zero value.
func (t PageToken) NextCursor(message proto.Message, paths ...string) (PageToken, error) {
	if len(paths) == 0 {
		return PageToken{}, errors.New("next cursor: no ordering paths")
	}
	reflected := message.ProtoReflect()
	cursor := make(Cursor, 0, len(paths))
	for _, path := range paths {
		leaf, err := protopath.Get(reflected, path)
		if err != nil {
			return PageToken{}, fmt.Errorf("next cursor: %w", err)
		}
		value, err := cursorValue(leaf)
		if err != nil {
			return PageToken{}, fmt.Errorf("next cursor: path %q: %w", path, err)
		}
		cursor = append(cursor, value)
	}
	t.Cursor = cursor
	return t, nil
}

// cursorValue converts a resolved proto field into its cursor representation.
func cursorValue(leaf protopath.Leaf) (any, error) {
	field := leaf.Field
	switch {
	case field.IsList():
		return nil, errors.New("repeated fields cannot be ordered on")
	case field.IsMap():
		return nil, errors.New("map fields cannot be ordered on")
	case !leaf.Present:
		return nil, nil
	}
	switch field.Kind() {
	case protoreflect.BoolKind:
		return leaf.Value.Bool(), nil
	case protoreflect.StringKind:
		return leaf.Value.String(), nil
	case protoreflect.BytesKind:
		return leaf.Value.Bytes(), nil
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return leaf.Value.Int(), nil
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return leaf.Value.Uint(), nil
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return leaf.Value.Float(), nil
	case protoreflect.EnumKind:
		// Enum numbers, not names: ordering follows declaration order, which
		// is what a database column storing the number will sort by.
		return int64(leaf.Value.Enum()), nil
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return wellKnownCursorValue(leaf.Value.Message())
	default:
		return nil, fmt.Errorf("unsupported field kind %s", field.Kind())
	}
}

// wellKnownCursorValue unwraps the well-known message types that carry a
// single orderable scalar. Any other message has no total order and is
// rejected.
func wellKnownCursorValue(message protoreflect.Message) (any, error) {
	switch name := message.Descriptor().FullName(); name {
	case "google.protobuf.Timestamp":
		timestamp, ok := message.Interface().(*timestamppb.Timestamp)
		if !ok {
			return nil, fmt.Errorf("%s: unexpected dynamic message", name)
		}
		return timestamp.AsTime(), nil
	case "google.protobuf.Duration":
		duration, ok := message.Interface().(*durationpb.Duration)
		if !ok {
			return nil, fmt.Errorf("%s: unexpected dynamic message", name)
		}
		return duration.AsDuration(), nil
	case "google.protobuf.StringValue", "google.protobuf.BytesValue",
		"google.protobuf.BoolValue", "google.protobuf.DoubleValue",
		"google.protobuf.FloatValue", "google.protobuf.Int32Value",
		"google.protobuf.Int64Value", "google.protobuf.UInt32Value",
		"google.protobuf.UInt64Value":
		field := message.Descriptor().Fields().ByName("value")
		return cursorValue(protopath.Leaf{Field: field, Value: message.Get(field), Present: true})
	default:
		return nil, fmt.Errorf("message type %s has no ordering", name)
	}
}

// Encode returns the opaque string form of the token, to be returned to the
// client as `next_page_token`.
//
// It reports an error rather than emitting a corrupt token when the cursor
// holds an unsupported value; see [Cursor] for the supported set.
func (t PageToken) Encode() (string, error) {
	buffer := make([]byte, 0, 32)
	buffer = append(buffer, pageTokenVersion)
	buffer = binary.AppendVarint(buffer, t.Offset)
	buffer = binary.LittleEndian.AppendUint32(buffer, t.RequestChecksum)
	buffer = binary.AppendUvarint(buffer, uint64(len(t.Cursor)))
	for i, value := range t.Cursor {
		var err error
		if buffer, err = appendCursorValue(buffer, value); err != nil {
			return "", fmt.Errorf("encode page token: cursor value %d: %w", i, err)
		}
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

// DecodePageToken decodes an encoded page token.
//
// It does not check the token against a request; [ParsePageToken] does that.
func DecodePageToken(s string) (PageToken, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return PageToken{}, fmt.Errorf("%w: %w", ErrMalformedPageToken, err)
	}
	if len(raw) == 0 {
		return PageToken{}, errTruncatedPageToken
	}
	if raw[0] != pageTokenVersion {
		return PageToken{}, fmt.Errorf("%w: unsupported version 0x%02x", ErrMalformedPageToken, raw[0])
	}
	raw = raw[1:]
	offset, n := binary.Varint(raw)
	if n <= 0 {
		return PageToken{}, errTruncatedPageToken
	}
	raw = raw[n:]
	if len(raw) < 4 {
		return PageToken{}, errTruncatedPageToken
	}
	checksum := binary.LittleEndian.Uint32(raw)
	raw = raw[4:]
	length, n := binary.Uvarint(raw)
	if n <= 0 {
		return PageToken{}, errTruncatedPageToken
	}
	raw = raw[n:]
	// Guard against a hostile length driving a huge allocation: every cursor
	// value costs at least one byte on the wire.
	if length > uint64(len(raw)) {
		return PageToken{}, errTruncatedPageToken
	}
	token := PageToken{Offset: offset, RequestChecksum: checksum}
	if length > 0 {
		token.Cursor = make(Cursor, 0, length)
		for range length {
			var value any
			if value, raw, err = consumeCursorValue(raw); err != nil {
				return PageToken{}, err
			}
			token.Cursor = append(token.Cursor, value)
		}
	}
	if len(raw) != 0 {
		return PageToken{}, fmt.Errorf("%w: %d trailing bytes", ErrMalformedPageToken, len(raw))
	}
	return token, nil
}

// checksumExemptFields are cleared before checksumming: they are expected to
// change from one page to the next, so including them would invalidate every
// token as soon as it was used.
var checksumExemptFields = [...]protoreflect.Name{"page_token", "page_size", "skip"}

// CalculateRequestChecksum returns a checksum over the fields of request that
// must stay identical across the pages of one result set.
//
// Two requests that differ only in `page_token`, `page_size` or `skip` have
// the same checksum; a request whose `filter` or `order_by` changed does not.
func CalculateRequestChecksum(request PageRequest) (uint32, error) {
	// Clone so that clearing the volatile fields cannot disturb the caller's
	// message.
	clone := proto.Clone(request).ProtoReflect()
	fields := clone.Descriptor().Fields()
	for _, name := range checksumExemptFields {
		if field := fields.ByName(name); field != nil {
			clone.Clear(field)
		}
	}
	// Deterministic marshaling: the default ordering is explicitly unstable,
	// which would make checksums over messages with map fields flap between
	// calls.
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(clone.Interface())
	if err != nil {
		return 0, fmt.Errorf("calculate request checksum: %w", err)
	}
	return crc32.ChecksumIEEE(data), nil
}
