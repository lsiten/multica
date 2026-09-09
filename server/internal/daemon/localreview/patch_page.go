package localreview

import (
	"bufio"
	"context"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
)

const patchPageBytes = 256 << 10
const patchLineBytes = 64 << 10

var ErrPatchLineTooLarge = errors.New("a diff line is too large to preview")
var ErrInvalidPatchPage = errors.New("invalid diff page range")
var patchHunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

type PatchPageRequest struct {
	Offset int64
	Limit  int
}

type PatchLine struct {
	Text            string `json:"text"`
	Kind            string `json:"kind"`
	OldLine         int64  `json:"old_line,omitempty"`
	NewLine         int64  `json:"new_line,omitempty"`
	ContextOldStart int64  `json:"context_old_start,omitempty"`
	ContextNewStart int64  `json:"context_new_start,omitempty"`
	ContextLines    int64  `json:"context_lines,omitempty"`
}

type PatchPage struct {
	Lines    []PatchLine `json:"lines"`
	NextLine int64       `json:"next_line"`
	HasMore  bool        `json:"has_more"`
}

// ReadPatchPage consumes a stable patch stream with bounded memory. Offsets count
// physical patch lines, including headers, so continuations preserve line numbers.
// The caller owns stream cancellation/closure and the immutable snapshot identity.
func ReadPatchPage(ctx context.Context, input io.Reader, request PatchPageRequest) (PatchPage, error) {
	page := PatchPage{Lines: []PatchLine{}, NextLine: request.Offset}
	if request.Offset < 0 || request.Limit < 1 || request.Limit > 500 {
		return page, ErrInvalidPatchPage
	}
	reader := bufio.NewReaderSize(input, patchLineBytes)
	oldLine, newLine := int64(1), int64(1)
	var index int64
	insideHunk, size := false, 0
	for {
		if err := ctx.Err(); err != nil {
			return page, err
		}
		line, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			return page, ErrPatchLineTooLarge
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return page, err
		}
		if len(line) == 0 {
			return page, nil
		}
		if index >= request.Offset && size+len(line) > patchPageBytes {
			page.HasMore = true
			return page, nil
		}
		text := strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r")
		entry := PatchLine{Text: text, Kind: "meta"}
		if match := patchHunkHeader.FindStringSubmatch(text); match != nil {
			previousOld, previousNew := oldLine, newLine
			oldLine, err = strconv.ParseInt(match[1], 10, 64)
			if err != nil {
				return page, ErrInvalidPatchPage
			}
			newLine, err = strconv.ParseInt(match[2], 10, 64)
			if err != nil {
				return page, ErrInvalidPatchPage
			}
			if gap := min(oldLine-previousOld, newLine-previousNew); gap > 0 {
				entry.ContextOldStart, entry.ContextNewStart, entry.ContextLines = previousOld, previousNew, gap
			}
			insideHunk = true
		} else if strings.HasPrefix(text, "diff --git ") {
			insideHunk = false
		} else if insideHunk && len(text) > 0 {
			switch text[0] {
			case ' ':
				entry.Kind, entry.OldLine, entry.NewLine = "context", oldLine, newLine
				oldLine++
				newLine++
			case '-':
				entry.Kind, entry.OldLine = "remove", oldLine
				oldLine++
			case '+':
				entry.Kind, entry.NewLine = "add", newLine
				newLine++
			}
		}
		index++
		if index <= request.Offset {
			continue
		}
		page.Lines = append(page.Lines, entry)
		page.NextLine = index
		size += len(line)
		if len(page.Lines) == request.Limit {
			_, peekErr := reader.Peek(1)
			if peekErr != nil && !errors.Is(peekErr, io.EOF) {
				return page, peekErr
			}
			page.HasMore = peekErr == nil
			return page, nil
		}
		if errors.Is(err, io.EOF) {
			return page, nil
		}
	}
}
