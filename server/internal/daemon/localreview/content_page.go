package localreview

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"strings"
	"unicode/utf8"
)

type CachedContentPage struct {
	Blob   BlobRef
	Offset int64
	Limit  int
	Binary bool
}
type ContentPage struct {
	Text       string `json:"text"`
	Encoding   string `json:"encoding"`
	Offset     int64  `json:"offset"`
	NextOffset int64  `json:"next_offset"`
	Size       int64  `json:"size"`
	HasMore    bool   `json:"has_more"`
}

// ContentPage exposes a bounded range only after full-stream integrity validation.
// Text ranges may extend by at most three bytes to preserve a UTF-8 code point.
func (s *BlobStore) ContentPage(ctx context.Context, request CachedContentPage) (ContentPage, error) {
	if request.Offset < 0 || request.Offset > request.Blob.Size || request.Limit < 1 || request.Limit > 64<<10 {
		return ContentPage{}, ErrInvalidPatchPage
	}
	selected := &contentRange{start: request.Offset, end: request.Offset + int64(request.Limit) + 3}
	err := s.consume(ctx, request.Blob, func(reader io.Reader) error {
		_, err := io.CopyBuffer(selected, reader, make([]byte, 64<<10))
		return err
	})
	if err != nil {
		return ContentPage{}, err
	}
	return renderContentPage(selected.data, contentWindow{Offset: request.Offset, Size: request.Blob.Size, Limit: request.Limit, Binary: request.Binary}), nil
}

type contentWindow struct {
	Offset, Size int64
	Limit        int
	Binary       bool
}

func renderContentPage(data []byte, request contentWindow) ContentPage {
	length := min(request.Limit, len(data))
	page := ContentPage{Encoding: "hex", Offset: request.Offset, Size: request.Size}
	if !request.Binary && bytes.IndexByte(data, 0) < 0 {
		for end := length; end <= len(data); end++ {
			if utf8.Valid(data[:end]) {
				length = end
				page.Encoding = "utf8"
				page.Text = string(data[:end])
				break
			}
		}
	}
	if page.Encoding == "hex" {
		page.Text = hexContent(data[:length])
	}
	page.NextOffset = request.Offset + int64(length)
	page.HasMore = page.NextOffset < page.Size
	return page
}

type contentRange struct {
	start, end, position int64
	data                 []byte
}

func (r *contentRange) Write(data []byte) (int, error) {
	start := max(int64(0), r.start-r.position)
	end := min(int64(len(data)), r.end-r.position)
	if start < end {
		r.data = append(r.data, data[start:end]...)
	}
	r.position += int64(len(data))
	return len(data), nil
}

func hexContent(data []byte) string {
	encoded := hex.EncodeToString(data)
	var output strings.Builder
	for i := range len(data) {
		if i > 0 {
			if i%16 == 0 {
				output.WriteByte('\n')
			} else {
				output.WriteByte(' ')
			}
		}
		output.WriteString(encoded[i*2 : i*2+2])
	}
	return output.String()
}
