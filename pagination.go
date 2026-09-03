package aip

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"time"

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
//   - Offset counts rows already returned. Advance it with [PageToken.NextOffset].
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
	Cursor PageCursor
	// RequestChecksum is the checksum of the request that produced this token.
	RequestChecksum uint32
}

// PageCursor is the ordered tuple of sort-key values identifying the last row of
// a page, for key-set (a.k.a. "seek") pagination.
//
// A cursor is only meaningful alongside the `order_by` that produced it: the
// i'th cursor value is the i'th ordering field's value on the last row, so
// len(cursor) must equal the number of ordering fields. Serving the next page
// means asking for rows that sort strictly after the tuple.
//
// Cursor values are restricted to the types listed below, because a page
// token crosses a process boundary and has to survive the round trip exactly.
// Building a cursor with anything else is an error at encode time — see
// [Cursor.Validate].
//
//	Go type in                      Go type out
//	----------------------------    -----------------------
//	nil                             nil
//	bool                            bool
//	string                          string
//	[]byte                          []byte
//	int, int8, int16, int32, int64  int64
//	uint, uint8, …, uint64          uint64
//	float32, float64                float64
//	time.Time                       time.Time (UTC)
//	time.Duration                   time.Duration
//
// Sized integers widen to int64/uint64 and float32 widens to float64, so a
// decoded cursor is not always ==-comparable with the value that produced it.
// The widening is lossless and keeps the wire format small; comparisons
// against a database column are unaffected.
type PageCursor []any

// PageCursor value type tags. These are wire format — never renumber them.
// Adding a type means appending a new tag and bumping [pageTokenVersion].
const (
	tagNil      byte = 0x00
	tagFalse    byte = 0x01
	tagTrue     byte = 0x02
	tagString   byte = 0x03
	tagBytes    byte = 0x04
	tagInt      byte = 0x05
	tagUint     byte = 0x06
	tagFloat    byte = 0x07
	tagTime     byte = 0x08
	tagDuration byte = 0x09
)

// Validate reports whether every value in the cursor is of a supported type.
//
// [PageToken.Encode] calls this, so checking up front is only necessary when
// you want to reject a bad cursor before building a token.
func (c PageCursor) Validate() error {
	for i, value := range c {
		if _, err := appendCursorValue(nil, value); err != nil {
			return fmt.Errorf("cursor value %d: %w", i, err)
		}
	}
	return nil
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

// NextOffset returns the token for the page following this one, under offset
// pagination, by advancing the offset past the page just served.
//
// Pair it with [PageToken.Offset]; the counterpart for key-set pagination is
// [PageToken.NextCursor]. Advancing the wrong one yields a token the query
// layer cannot serve correctly, so choose per List method and stay with it.
func (t PageToken) NextOffset(request PageRequest) PageToken {
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
	cursor := make(PageCursor, 0, len(paths))
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
// holds an unsupported value; see [PageCursor] for the supported set.
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
		token.Cursor = make(PageCursor, 0, length)
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

func appendCursorValue(dst []byte, value any) ([]byte, error) {
	switch v := value.(type) {
	case nil:
		return append(dst, tagNil), nil
	case bool:
		if v {
			return append(dst, tagTrue), nil
		}
		return append(dst, tagFalse), nil
	case string:
		dst = append(dst, tagString)
		dst = binary.AppendUvarint(dst, uint64(len(v)))
		return append(dst, v...), nil
	case []byte:
		dst = append(dst, tagBytes)
		dst = binary.AppendUvarint(dst, uint64(len(v)))
		return append(dst, v...), nil
	// time.Duration precedes the integer cases: its underlying type is int64,
	// but a type switch matches the named type, and we want the named tag so
	// it decodes back as a Duration rather than a bare int64.
	case time.Duration:
		dst = append(dst, tagDuration)
		return binary.AppendVarint(dst, int64(v)), nil
	case time.Time:
		dst = append(dst, tagTime)
		utc := v.UTC()
		dst = binary.AppendVarint(dst, utc.Unix())
		return binary.AppendVarint(dst, int64(utc.Nanosecond())), nil
	case int:
		return appendCursorInt(dst, int64(v)), nil
	case int8:
		return appendCursorInt(dst, int64(v)), nil
	case int16:
		return appendCursorInt(dst, int64(v)), nil
	case int32:
		return appendCursorInt(dst, int64(v)), nil
	case int64:
		return appendCursorInt(dst, v), nil
	case uint:
		return appendCursorUint(dst, uint64(v)), nil
	case uint8:
		return appendCursorUint(dst, uint64(v)), nil
	case uint16:
		return appendCursorUint(dst, uint64(v)), nil
	case uint32:
		return appendCursorUint(dst, uint64(v)), nil
	case uint64:
		return appendCursorUint(dst, v), nil
	case float32:
		return appendCursorFloat(dst, float64(v)), nil
	case float64:
		return appendCursorFloat(dst, v), nil
	default:
		return nil, fmt.Errorf("unsupported type %T (see aip.PageCursor for the supported set)", value)
	}
}

func appendCursorInt(dst []byte, v int64) []byte {
	return binary.AppendVarint(append(dst, tagInt), v)
}

func appendCursorUint(dst []byte, v uint64) []byte {
	return binary.AppendUvarint(append(dst, tagUint), v)
}

func appendCursorFloat(dst []byte, v float64) []byte {
	return binary.LittleEndian.AppendUint64(append(dst, tagFloat), math.Float64bits(v))
}

func consumeCursorValue(src []byte) (any, []byte, error) {
	if len(src) == 0 {
		return nil, nil, errTruncatedPageToken
	}
	tag, src := src[0], src[1:]
	switch tag {
	case tagNil:
		return nil, src, nil
	case tagFalse:
		return false, src, nil
	case tagTrue:
		return true, src, nil
	case tagString, tagBytes:
		length, n := binary.Uvarint(src)
		if n <= 0 {
			return nil, nil, errTruncatedPageToken
		}
		src = src[n:]
		if uint64(len(src)) < length {
			return nil, nil, errTruncatedPageToken
		}
		payload, rest := src[:length], src[length:]
		if tag == tagString {
			return string(payload), rest, nil
		}
		// Copy: the caller must not be able to observe later writes to src.
		return append([]byte(nil), payload...), rest, nil
	case tagInt:
		v, n := binary.Varint(src)
		if n <= 0 {
			return nil, nil, errTruncatedPageToken
		}
		return v, src[n:], nil
	case tagUint:
		v, n := binary.Uvarint(src)
		if n <= 0 {
			return nil, nil, errTruncatedPageToken
		}
		return v, src[n:], nil
	case tagFloat:
		if len(src) < 8 {
			return nil, nil, errTruncatedPageToken
		}
		return math.Float64frombits(binary.LittleEndian.Uint64(src)), src[8:], nil
	case tagDuration:
		v, n := binary.Varint(src)
		if n <= 0 {
			return nil, nil, errTruncatedPageToken
		}
		return time.Duration(v), src[n:], nil
	case tagTime:
		seconds, n := binary.Varint(src)
		if n <= 0 {
			return nil, nil, errTruncatedPageToken
		}
		src = src[n:]
		nanos, n := binary.Varint(src)
		if n <= 0 {
			return nil, nil, errTruncatedPageToken
		}
		return time.Unix(seconds, nanos).UTC(), src[n:], nil
	default:
		return nil, nil, fmt.Errorf("unknown cursor value tag 0x%02x", tag)
	}
}
