package aip

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

// Cursor is the ordered tuple of sort-key values identifying the last row of
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
type Cursor []any

// Cursor value type tags. These are wire format — never renumber them.
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
func (c Cursor) Validate() error {
	for i, value := range c {
		if _, err := appendCursorValue(nil, value); err != nil {
			return fmt.Errorf("cursor value %d: %w", i, err)
		}
	}
	return nil
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
		return nil, fmt.Errorf("unsupported type %T (see aip.Cursor for the supported set)", value)
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
