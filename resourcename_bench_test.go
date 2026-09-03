package aip_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/protoc-contrib/aip-go"
)

var benchPattern = aip.MustCompileResourcePattern("publishers/{publisher}/books/{book}")

// Package-level so the compiler cannot treat them as constants.
var (
	benchName      = "publishers/p1/books/b1"
	benchPublisher = "p1"
	benchBook      = "b1"

	// sink defeats dead-code elimination: without it the compiler can drop
	// a discarded concatenation entirely and the comparison reads as free.
	sink string
)

// BenchmarkScan pins the allocation-free claim in ResourcePattern.Scan's doc.
func BenchmarkScan(b *testing.B) {
	b.ReportAllocs()
	var publisher, book string
	for b.Loop() {
		if err := benchPattern.Scan(benchName, &publisher, &book); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkScanSplit is the strings.Split shape the generator inlines today,
// for comparison.
func BenchmarkScanSplit(b *testing.B) {
	b.ReportAllocs()
	var publisher, book string
	for b.Loop() {
		parts := strings.Split(benchName, "/")
		if len(parts) != 4 {
			b.Fatal("bad segments")
		}
		if parts[0] != "publishers" || parts[2] != "books" {
			b.Fatal("bad literal")
		}
		publisher, book = parts[1], parts[3]
	}
	_, _ = publisher, book
}

func BenchmarkFormat(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		sink = benchPattern.Format(benchPublisher, benchBook)
	}
}

// BenchmarkFormatConcat is the string-concatenation shape the generator
// inlines today, for comparison. The IDs come from variables, not literals —
// with constant operands the compiler folds the whole expression away and the
// comparison is meaningless.
func BenchmarkFormatConcat(b *testing.B) {
	b.ReportAllocs()
	publisher, book := benchPublisher, benchBook
	for b.Loop() {
		sink = "publishers/" + publisher + "/books/" + book
	}
}

func BenchmarkFormatSprintf(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		sink = fmt.Sprintf("publishers/%s/books/%s", benchPublisher, benchBook)
	}
}
