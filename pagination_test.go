package aip_test

import (
	"errors"
	"math"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/protoc-contrib/aip-go"
	testpb "github.com/protoc-contrib/aip-go/internal/testpb"
)

var _ = Describe("PageToken", func() {
	Describe("Encode/Decode round trip", func() {
		DescribeTable("preserves the token exactly",
			func(token aip.PageToken) {
				encoded, err := token.Encode()
				Expect(err).NotTo(HaveOccurred())

				decoded, err := aip.DecodePageToken(encoded)
				Expect(err).NotTo(HaveOccurred())
				Expect(decoded).To(Equal(token))
			},
			Entry("zero token", aip.PageToken{}),
			Entry("offset only", aip.PageToken{Offset: 42, RequestChecksum: 0xdeadbeef}),
			Entry("large offset", aip.PageToken{Offset: math.MaxInt64}),
			Entry("negative offset", aip.PageToken{Offset: -1}),
			Entry("max checksum", aip.PageToken{RequestChecksum: math.MaxUint32}),
			Entry("string cursor", aip.PageToken{
				Offset: 10, Cursor: aip.Cursor{"Bob", "uuid-7"}, RequestChecksum: 0xdeadbeef,
			}),
			// The case that silently corrupted the token under gob.
			Entry("timestamp cursor", aip.PageToken{
				Offset: 10, Cursor: aip.Cursor{time.Unix(1700000000, 123456789).UTC()},
			}),
			Entry("duration cursor", aip.PageToken{Cursor: aip.Cursor{5 * time.Second}}),
			Entry("mixed cursor", aip.PageToken{
				Offset: 3,
				Cursor: aip.Cursor{
					"Alice", int64(7), uint64(9), 1.5, true, false, nil,
					[]byte{0x00, 0xff}, time.Unix(0, 0).UTC(), time.Duration(0),
				},
				RequestChecksum: 1,
			}),
			Entry("nil-only cursor", aip.PageToken{Cursor: aip.Cursor{nil, nil}}),
			Entry("empty string cursor", aip.PageToken{Cursor: aip.Cursor{""}}),
			Entry("min int cursor", aip.PageToken{Cursor: aip.Cursor{int64(math.MinInt64)}}),
			Entry("max uint cursor", aip.PageToken{Cursor: aip.Cursor{uint64(math.MaxUint64)}}),
		)

		It("widens sized numeric types to their 64-bit form", func() {
			token := aip.PageToken{Cursor: aip.Cursor{int32(1), uint32(2), float32(0.5)}}
			encoded, err := token.Encode()
			Expect(err).NotTo(HaveOccurred())

			decoded, err := aip.DecodePageToken(encoded)
			Expect(err).NotTo(HaveOccurred())
			Expect(decoded.Cursor).To(Equal(aip.Cursor{int64(1), uint64(2), float64(0.5)}))
		})

		It("normalizes timestamps to UTC", func() {
			zone := time.FixedZone("UTC+4", 4*60*60)
			token := aip.PageToken{Cursor: aip.Cursor{time.Unix(1700000000, 0).In(zone)}}
			encoded, err := token.Encode()
			Expect(err).NotTo(HaveOccurred())

			decoded, err := aip.DecodePageToken(encoded)
			Expect(err).NotTo(HaveOccurred())
			Expect(decoded.Cursor[0]).To(BeTemporally("==", time.Unix(1700000000, 0)))
			Expect(decoded.Cursor[0].(time.Time).Location()).To(Equal(time.UTC))
		})

		It("produces URL-safe tokens", func() {
			token := aip.PageToken{
				Offset:          math.MaxInt64,
				Cursor:          aip.Cursor{"a/b+c=d?e&f"},
				RequestChecksum: math.MaxUint32,
			}
			encoded, err := token.Encode()
			Expect(err).NotTo(HaveOccurred())
			Expect(encoded).To(MatchRegexp(`^[A-Za-z0-9_-]+$`))
		})
	})

	Describe("Encode", func() {
		It("reports an error instead of emitting a corrupt token", func() {
			token := aip.PageToken{Cursor: aip.Cursor{struct{ A int }{1}}}

			encoded, err := token.Encode()
			Expect(err).To(MatchError(ContainSubstring("cursor value 0")))
			Expect(err).To(MatchError(ContainSubstring("unsupported type")))
			Expect(encoded).To(BeEmpty())
		})
	})

	Describe("DecodePageToken", func() {
		DescribeTable("rejects malformed input",
			func(encoded string) {
				_, err := aip.DecodePageToken(encoded)
				Expect(err).To(MatchError(aip.ErrMalformedPageToken))
			},
			Entry("empty", ""),
			Entry("not base64", "!!!!"),
			Entry("unknown version", "_wA"),
			Entry("truncated after version", "AQ"),
		)

		It("rejects a token with trailing bytes", func() {
			encoded, err := aip.PageToken{Offset: 1}.Encode()
			Expect(err).NotTo(HaveOccurred())

			_, err = aip.DecodePageToken(encoded + "AAAA")
			Expect(err).To(MatchError(aip.ErrMalformedPageToken))
		})

		It("rejects a cursor length beyond the remaining bytes", func() {
			_, err := aip.DecodePageToken("AQAAAAAA_____w8")
			Expect(err).To(MatchError(aip.ErrMalformedPageToken))
		})
	})

	Describe("ParsePageToken", func() {
		It("returns a zero token carrying the request checksum on the first page", func() {
			request := &testpb.ListBooksRequest{Parent: "shelves/1", PageSize: 10}

			token, err := aip.ParsePageToken(request)
			Expect(err).NotTo(HaveOccurred())
			Expect(token.Offset).To(BeZero())
			Expect(token.Cursor).To(BeEmpty())
			Expect(token.RequestChecksum).NotTo(BeZero())
		})

		It("round-trips a token through a subsequent request", func() {
			request := &testpb.ListBooksRequest{Parent: "shelves/1", PageSize: 10, Filter: "a=b"}
			first, err := aip.ParsePageToken(request)
			Expect(err).NotTo(HaveOccurred())

			encoded, err := first.Next(request).Encode()
			Expect(err).NotTo(HaveOccurred())

			next := &testpb.ListBooksRequest{
				Parent: "shelves/1", PageSize: 10, Filter: "a=b", PageToken: encoded,
			}
			second, err := aip.ParsePageToken(next)
			Expect(err).NotTo(HaveOccurred())
			Expect(second.Offset).To(Equal(int64(10)))
		})

		It("rejects a token whose request changed materially", func() {
			request := &testpb.ListBooksRequest{Parent: "shelves/1", PageSize: 10, Filter: "a=b"}
			token, err := aip.ParsePageToken(request)
			Expect(err).NotTo(HaveOccurred())
			encoded, err := token.Next(request).Encode()
			Expect(err).NotTo(HaveOccurred())

			tampered := &testpb.ListBooksRequest{
				Parent: "shelves/1", PageSize: 10, Filter: "a=c", PageToken: encoded,
			}
			_, err = aip.ParsePageToken(tampered)
			Expect(errors.Is(err, aip.ErrChecksumMismatch)).To(BeTrue())
		})

		It("accepts a changed page_size", func() {
			request := &testpb.ListBooksRequest{Parent: "shelves/1", PageSize: 10}
			token, err := aip.ParsePageToken(request)
			Expect(err).NotTo(HaveOccurred())
			encoded, err := token.Next(request).Encode()
			Expect(err).NotTo(HaveOccurred())

			resized := &testpb.ListBooksRequest{Parent: "shelves/1", PageSize: 50, PageToken: encoded}
			next, err := aip.ParsePageToken(resized)
			Expect(err).NotTo(HaveOccurred())
			Expect(next.Offset).To(Equal(int64(10)))
		})

		It("applies skip on requests that support it", func() {
			request := &testpb.ListBooksWithSkipRequest{Parent: "shelves/1", PageSize: 10, Skip: 5}

			token, err := aip.ParsePageToken(request)
			Expect(err).NotTo(HaveOccurred())
			Expect(token.Offset).To(Equal(int64(5)))
		})
	})

	Describe("CalculateRequestChecksum", func() {
		It("is stable across requests differing only in a map's insertion order", func() {
			// Map iteration order is randomized; without deterministic
			// marshaling this checksum would flap between calls.
			first := &testpb.ListBooksRequest{Labels: map[string]string{
				"a": "1", "b": "2", "c": "3", "d": "4", "e": "5", "f": "6", "g": "7", "h": "8",
			}}
			second := &testpb.ListBooksRequest{Labels: map[string]string{
				"h": "8", "g": "7", "f": "6", "e": "5", "d": "4", "c": "3", "b": "2", "a": "1",
			}}
			for range 20 {
				a, err := aip.CalculateRequestChecksum(first)
				Expect(err).NotTo(HaveOccurred())
				b, err := aip.CalculateRequestChecksum(second)
				Expect(err).NotTo(HaveOccurred())
				Expect(a).To(Equal(b))
			}
		})
	})

	Describe("NextCursor", func() {
		var book *testpb.Book

		BeforeEach(func() {
			book = &testpb.Book{
				Name:         "books/1",
				Title:        "Dune",
				Author:       &testpb.Author{Name: "Herbert", Country: "US"},
				ReadCount:    7,
				PageCount:    412,
				Checksum:     99,
				Rating:       4.5,
				Published:    true,
				Cover:        []byte{0x01, 0x02},
				Genre:        testpb.Genre_GENRE_FICTION,
				CreateTime:   timestamppb.New(time.Unix(1700000000, 0).UTC()),
				ReadDuration: durationpb.New(90 * time.Minute),
				Subtitle:     wrapperspb.String("a novel"),
			}
		})

		It("extracts fields in path order", func() {
			token, err := aip.PageToken{}.NextCursor(book, "title", "name")
			Expect(err).NotTo(HaveOccurred())
			Expect(token.Cursor).To(Equal(aip.Cursor{"Dune", "books/1"}))
		})

		DescribeTable("maps each field kind to its cursor value",
			func(path string, expected any) {
				token, err := aip.PageToken{}.NextCursor(book, path)
				Expect(err).NotTo(HaveOccurred())
				Expect(token.Cursor).To(HaveLen(1))
				Expect(token.Cursor[0]).To(Equal(expected))
			},
			Entry("string", "title", "Dune"),
			Entry("int32 widens", "read_count", int64(7)),
			Entry("int64", "page_count", int64(412)),
			Entry("uint64", "checksum", uint64(99)),
			Entry("double", "rating", 4.5),
			Entry("bool", "published", true),
			Entry("bytes", "cover", []byte{0x01, 0x02}),
			Entry("enum becomes its number", "genre", int64(1)),
			Entry("timestamp becomes time.Time", "create_time", time.Unix(1700000000, 0).UTC()),
			Entry("duration becomes time.Duration", "read_duration", 90*time.Minute),
			Entry("wrapper unwraps", "subtitle", "a novel"),
		)

		It("resolves dotted paths into nested messages", func() {
			token, err := aip.PageToken{}.NextCursor(book, "author.name", "author.country")
			Expect(err).NotTo(HaveOccurred())
			Expect(token.Cursor).To(Equal(aip.Cursor{"Herbert", "US"}))
		})

		It("yields nil for an unset message field rather than a zero value", func() {
			book.CreateTime = nil

			token, err := aip.PageToken{}.NextCursor(book, "create_time")
			Expect(err).NotTo(HaveOccurred())
			Expect(token.Cursor).To(Equal(aip.Cursor{nil}))
		})

		It("yields nil for an unset optional scalar", func() {
			token, err := aip.PageToken{}.NextCursor(book, "edition")
			Expect(err).NotTo(HaveOccurred())
			Expect(token.Cursor).To(Equal(aip.Cursor{nil}))
		})

		It("yields nil when an intermediate message on the path is unset", func() {
			book.Author = nil

			token, err := aip.PageToken{}.NextCursor(book, "author.name")
			Expect(err).NotTo(HaveOccurred())
			Expect(token.Cursor).To(Equal(aip.Cursor{nil}))
		})

		It("treats an explicit zero on a presence-less field as a real value", func() {
			book.ReadCount = 0

			token, err := aip.PageToken{}.NextCursor(book, "read_count")
			Expect(err).NotTo(HaveOccurred())
			Expect(token.Cursor).To(Equal(aip.Cursor{int64(0)}))
		})

		It("preserves the offset and checksum it was called on", func() {
			token, err := aip.PageToken{Offset: 5, RequestChecksum: 7}.NextCursor(book, "title")
			Expect(err).NotTo(HaveOccurred())
			Expect(token.Offset).To(Equal(int64(5)))
			Expect(token.RequestChecksum).To(Equal(uint32(7)))
		})

		DescribeTable("rejects paths that cannot produce an ordering",
			func(path, message string) {
				_, err := aip.PageToken{}.NextCursor(book, path)
				Expect(err).To(MatchError(ContainSubstring(message)))
			},
			Entry("unknown field", "nope", `no field "nope"`),
			Entry("unknown nested field", "author.nope", `no field "nope"`),
			Entry("repeated field", "tags", "repeated fields cannot be ordered on"),
			Entry("map field", "labels", "map fields cannot be ordered on"),
			Entry("message without an ordering", "author", "has no ordering"),
			Entry("traversal through a scalar", "title.length", "cannot traverse into scalar"),
			Entry("traversal through a repeated field", "tags.0", "cannot traverse into repeated"),
			Entry("empty path", "", "empty path"),
		)

		It("requires at least one path", func() {
			_, err := aip.PageToken{}.NextCursor(book)
			Expect(err).To(MatchError(ContainSubstring("no ordering paths")))
		})

		It("survives the full round trip through an encoded token", func() {
			token, err := aip.PageToken{}.NextCursor(book, "create_time", "read_duration", "name")
			Expect(err).NotTo(HaveOccurred())

			encoded, err := token.Encode()
			Expect(err).NotTo(HaveOccurred())

			decoded, err := aip.DecodePageToken(encoded)
			Expect(err).NotTo(HaveOccurred())
			Expect(decoded.Cursor).To(Equal(aip.Cursor{
				time.Unix(1700000000, 0).UTC(), 90 * time.Minute, "books/1",
			}))
		})

		It("lines up with the paths of the order_by that produced it", func() {
			request := &testpb.ListBooksRequest{OrderBy: "author.name, create_time desc, name"}
			orderBy, err := aip.ParseOrderBy(request)
			Expect(err).NotTo(HaveOccurred())

			token, err := aip.PageToken{}.NextCursor(book, orderBy.Paths()...)
			Expect(err).NotTo(HaveOccurred())
			Expect(token.Cursor).To(HaveLen(len(orderBy.Fields)))
			Expect(token.Cursor).To(Equal(aip.Cursor{
				"Herbert", time.Unix(1700000000, 0).UTC(), "books/1",
			}))
		})
	})

	Describe("Next", func() {
		It("advances the offset by the page size", func() {
			request := &testpb.ListBooksRequest{PageSize: 25}

			token := aip.PageToken{Offset: 50, RequestChecksum: 3}.Next(request)
			Expect(token.Offset).To(Equal(int64(75)))
			Expect(token.RequestChecksum).To(Equal(uint32(3)))
		})
	})
})

var _ = Describe("Cursor", func() {
	Describe("Validate", func() {
		It("accepts every supported type", func() {
			cursor := aip.Cursor{
				nil, true, "s", []byte{1}, 1, int8(1), int16(1), int32(1), int64(1),
				uint(1), uint8(1), uint16(1), uint32(1), uint64(1),
				float32(1), float64(1), time.Now(), time.Second,
			}
			Expect(cursor.Validate()).To(Succeed())
		})

		It("names the offending index", func() {
			cursor := aip.Cursor{"ok", make(chan int)}
			Expect(cursor.Validate()).To(MatchError(ContainSubstring("cursor value 1")))
		})
	})
})
