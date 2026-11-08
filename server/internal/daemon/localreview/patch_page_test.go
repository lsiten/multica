package localreview

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestPatchPageKeepsLineNumbersAcrossPages(t *testing.T) {
	patch := "@@ -10,2 +20,2 @@\n same\n-before\n+after\n"
	first, err := ReadPatchPage(t.Context(), strings.NewReader(patch), PatchPageRequest{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !first.HasMore || first.NextLine != 2 || first.Lines[1].OldLine != 10 || first.Lines[1].NewLine != 20 {
		t.Fatalf("unexpected first page: %+v", first)
	}
	second, err := ReadPatchPage(t.Context(), strings.NewReader(patch), PatchPageRequest{Offset: first.NextLine, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if second.HasMore || len(second.Lines) != 2 || second.Lines[0].Kind != "remove" || second.Lines[0].OldLine != 11 || second.Lines[1].Kind != "add" || second.Lines[1].NewLine != 21 {
		t.Fatalf("unexpected second page: %+v", second)
	}
}

func TestPatchPageDoesNotLoadAnEntireLargePatch(t *testing.T) {
	patch := "@@ -1 +1,900001 @@\n" + strings.Repeat("+a modest added line\n", 900000)
	input := strings.NewReader(patch)
	page, err := ReadPatchPage(t.Context(), input, PatchPageRequest{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Lines) != 50 || !page.HasMore {
		t.Fatalf("unexpected page: %+v", page)
	}
	if consumed := len(patch) - input.Len(); consumed > patchLineBytes {
		t.Fatalf("first page consumed %d bytes instead of bounded lookahead", consumed)
	}
}

func TestPatchPageByteBoundaryDoesNotDropTheNextLine(t *testing.T) {
	patch := strings.Repeat("+"+strings.Repeat("x", 60<<10)+"\n", 5)
	page, err := ReadPatchPage(t.Context(), strings.NewReader(patch), PatchPageRequest{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Lines) != 4 || page.NextLine != 4 || !page.HasMore {
		t.Fatalf("unexpected first page: %+v", page)
	}
	last, err := ReadPatchPage(t.Context(), strings.NewReader(patch), PatchPageRequest{Offset: page.NextLine, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Lines) != 1 || last.HasMore || last.NextLine != 5 {
		t.Fatalf("unexpected last page: %+v", last)
	}
}

func TestPatchPageFinalLineWithoutNewline(t *testing.T) {
	page, err := ReadPatchPage(t.Context(), strings.NewReader("@@ -0,0 +1 @@\n+last"), PatchPageRequest{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.HasMore || len(page.Lines) != 2 || page.Lines[1].NewLine != 1 || page.Lines[1].Text != "+last" {
		t.Fatalf("unexpected page: %+v", page)
	}
}

func TestPatchPageRejectsOversizedSingleLineWithoutUnboundedAllocation(t *testing.T) {
	_, err := ReadPatchPage(t.Context(), strings.NewReader("+"+strings.Repeat("x", 128<<10)), PatchPageRequest{Limit: 20})
	if !errors.Is(err, ErrPatchLineTooLarge) {
		t.Fatalf("expected line limit, got %v", err)
	}
}

func TestPatchPageHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := ReadPatchPage(ctx, strings.NewReader("+line\n"), PatchPageRequest{Limit: 20})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func BenchmarkReadPatchPageDeepPage(b *testing.B) {
	patch := "@@ -0,0 +1,500000 @@\n" + strings.Repeat("+large review fixture line\n", 500000)
	request := PatchPageRequest{Offset: 250000, Limit: 200}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		page, err := ReadPatchPage(b.Context(), strings.NewReader(patch), request)
		if err != nil {
			b.Fatal(err)
		}
		if len(page.Lines) != 200 || page.Lines[0].NewLine != 250000 || page.Lines[199].NewLine != 250199 {
			b.Fatalf("wrong deep page: %+v", page)
		}
	}
}

func TestPatchPageSkippedLinesPreserveHunksAndFileBoundaries(t *testing.T) {
	patch := "diff --git a/a b/a\r\n--- a/a\r\n+++ b/a\r\n@@ -10,2 +20,2 @@\r\n same\r\n-before\r\n+after\r\n\\ No newline at end of file\r\n@@ -15 +25 @@\r\n tail\r\ndiff --git a/b b/b\r\n--- a/b\r\n+++ b/b\r\n@@ -1 +1,2 @@\r\n base\r\n+last"
	page, err := ReadPatchPage(t.Context(), strings.NewReader(patch), PatchPageRequest{Offset: 8, Limit: 8})
	if err != nil {
		t.Fatal(err)
	}
	want := PatchPage{NextLine: 16, Lines: []PatchLine{
		{Text: "@@ -15 +25 @@", Kind: "meta", ContextOldStart: 12, ContextNewStart: 22, ContextLines: 3},
		{Text: " tail", Kind: "context", OldLine: 15, NewLine: 25},
		{Text: "diff --git a/b b/b", Kind: "meta"},
		{Text: "--- a/b", Kind: "meta"},
		{Text: "+++ b/b", Kind: "meta"},
		{Text: "@@ -1 +1,2 @@", Kind: "meta"},
		{Text: " base", Kind: "context", OldLine: 1, NewLine: 1},
		{Text: "+last", Kind: "add", NewLine: 2},
	}}
	if !reflect.DeepEqual(page, want) {
		t.Fatalf("deep page changed line or hunk identity: got %+v, want %+v", page, want)
	}
}
