package localreview

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"strconv"
)

type sourceContentRead struct {
	Window         contentWindow
	SHA256, GitOID string
}

func streamSourceContent(ctx context.Context, input io.Reader, request sourceContentRead) (ContentPage, error) {
	window := request.Window
	if window.Offset < 0 || window.Offset > window.Size || window.Size < 0 || window.Size > 1<<40 || window.Limit < 1 || window.Limit > 64<<10 {
		return ContentPage{}, ErrInvalidPatchPage
	}
	var fingerprint hash.Hash
	expected := request.SHA256
	if expected != "" {
		if !validBlobID(expected) {
			return ContentPage{}, ErrInvalidSnapshotBlob
		}
		fingerprint = sha256.New()
	} else {
		expected = request.GitOID
		if !validGitObjectID(expected) {
			return ContentPage{}, ErrInvalidSnapshotBlob
		}
		if len(expected) == 40 {
			fingerprint = sha1.New()
		} else {
			fingerprint = sha256.New()
		}
		if _, err := io.WriteString(fingerprint, "blob "+strconv.FormatInt(window.Size, 10)+"\x00"); err != nil {
			return ContentPage{}, err
		}
	}
	selected := &contentRange{start: window.Offset, end: window.Offset + int64(window.Limit) + 3}
	size, err := io.CopyBuffer(io.MultiWriter(fingerprint, selected), io.LimitReader(reviewContextReader{ctx, input}, window.Size+1), make([]byte, 64<<10))
	if err != nil {
		return ContentPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return ContentPage{}, err
	}
	if size != window.Size || hex.EncodeToString(fingerprint.Sum(nil)) != expected {
		return ContentPage{}, ErrSnapshotContentChanged
	}
	return renderContentPage(selected.data, window), nil
}
