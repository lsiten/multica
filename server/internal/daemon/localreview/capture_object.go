package localreview

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"time"
)

type gitObjectRequest struct{ Repository, OID string }
type binaryProbe struct {
	reader io.Reader
	binary bool
}

func (p *binaryProbe) Read(data []byte) (int, error) {
	n, err := p.reader.Read(data)
	p.binary = p.binary || bytes.IndexByte(data[:n], 0) >= 0
	return n, err
}

func (s *BlobStore) captureObject(ctx context.Context, request gitObjectRequest) (BlobRef, bool, error) {
	var blob BlobRef
	binary := false
	err := readGitObject(ctx, request, func(reader io.Reader) error {
		probe := &binaryProbe{reader: reader}
		var err error
		blob, err = s.Put(ctx, probe)
		binary = probe.binary
		return err
	})
	return blob, binary, err
}

// readGitObject does not honor replacement refs: the requested object ID is fixed.
func readGitObject(ctx context.Context, request gitObjectRequest, consume func(io.Reader) error) error {
	if !validGitObjectID(request.OID) {
		return ErrInvalidReviewVersion
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "--no-pager", "-c", "core.hooksPath="+os.DevNull, "-c", "core.fsmonitor=false", "-C", request.Repository, "cat-file", "blob", request.OID)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_REPLACE_OBJECTS=1")
	cmd.Stderr = io.Discard
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		stdout.Close()
		return err
	}
	readErr := consume(stdout)
	if readErr != nil {
		cancel()
	}
	stdout.Close()
	waitErr := cmd.Wait()
	if readErr != nil {
		return readErr
	}
	return waitErr
}
