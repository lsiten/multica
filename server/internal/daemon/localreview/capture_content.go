package localreview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
)

type CapturedContent struct {
	Blob         BlobRef
	Cached       bool
	CacheLimited bool
	Binary       bool
	Lines        int64
}

type contentProbe struct {
	reader      io.Reader
	size, lines int64
	last        byte
	binary      bool
}

func (p *contentProbe) Read(data []byte) (int, error) {
	n, err := p.reader.Read(data)
	if n > 0 {
		p.size += int64(n)
		p.lines += int64(bytes.Count(data[:n], []byte{'\n'}))
		p.last = data[n-1]
		p.binary = p.binary || bytes.IndexByte(data[:n], 0) >= 0
	}
	return n, err
}

// CaptureContent fingerprints the entire input even when its cache quota is exceeded.
// The missing cached body is explicit; no partial body is published as a snapshot.
func (s *BlobStore) CaptureContent(ctx context.Context, input io.Reader) (CapturedContent, error) {
	hash := sha256.New()
	probe := &contentProbe{reader: io.TeeReader(io.LimitReader(reviewContextReader{ctx, input}, (1<<40)+1), hash)}
	blob, err := s.Put(ctx, probe)
	cached := err == nil
	cacheLimited := errors.Is(err, ErrSnapshotCacheFull)
	if errors.Is(err, ErrSnapshotBlobTooLarge) || cacheLimited {
		if _, err = io.CopyBuffer(io.Discard, probe, make([]byte, 64<<10)); err != nil {
			return CapturedContent{}, err
		}
	} else if err != nil {
		return CapturedContent{}, err
	}
	if probe.size > 1<<40 {
		return CapturedContent{}, ErrSnapshotBlobTooLarge
	}
	if !cached {
		blob = BlobRef{ID: hex.EncodeToString(hash.Sum(nil)), Size: probe.size}
	}
	if probe.size > 0 && probe.last != '\n' {
		probe.lines++
	}
	return CapturedContent{Blob: blob, Cached: cached, CacheLimited: cacheLimited, Binary: probe.binary, Lines: probe.lines}, nil
}
