package localreview

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
)

// ContextPage selects original file lines rather than patch lines. The enclosing
// blob read validates the entire immutable stream before returning any content.
func (s *BlobStore) ContextPage(ctx context.Context, request CachedPatchPage) (PatchPage, error) {
	page := PatchPage{Lines: []PatchLine{}, NextLine: request.Page.Offset}
	if request.Page.Offset < 0 || request.Page.Limit < 1 || request.Page.Limit > 500 {
		return PatchPage{}, ErrInvalidPatchPage
	}
	err := s.consume(ctx, request.Blob, func(input io.Reader) error {
		reader := bufio.NewReaderSize(input, patchLineBytes)
		var index int64
		size := 0
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			line, err := reader.ReadSlice('\n')
			if errors.Is(err, bufio.ErrBufferFull) {
				return ErrPatchLineTooLarge
			}
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			if len(line) == 0 {
				return nil
			}
			if index >= request.Page.Offset {
				if len(page.Lines) == request.Page.Limit || size+len(line) > patchPageBytes {
					page.HasMore = true
					return nil
				}
				page.Lines = append(page.Lines, PatchLine{Text: strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r"), Kind: "context", NewLine: index + 1})
				page.NextLine = index + 1
				size += len(line)
			}
			index++
			if errors.Is(err, io.EOF) {
				return nil
			}
		}
	})
	if err != nil {
		return PatchPage{}, err
	}
	return page, nil
}
