package localreview

import (
	"context"
	"errors"
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
