package jevmodels

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type progressWriter struct {
	writer io.Writer
	update func(int64)
}

func (p progressWriter) Write(b []byte) (int, error) {
	n, err := p.writer.Write(b)
	p.update(int64(n))
	return n, err
}

func downloadFile(ctx context.Context, client *http.Client, target string, f modelFile, update func(int64)) (err error) {
	u := "https://huggingface.co/" + ModelID + "/resolve/" + Revision + "/" + f.Name
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("model download HTTP %d", res.StatusCode)
	}
	file, err := os.OpenFile(filepath.Join(target, f.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	h := sha256.New()
	n, err := io.Copy(progressWriter{writer: io.MultiWriter(file, h), update: update}, io.LimitReader(res.Body, f.Size+1))
	if err != nil {
		return err
	}
	if n != f.Size || hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
		return fmt.Errorf("model integrity verification failed: %s", f.Name)
	}
	return file.Sync()
}

func verifyFiles(ctx context.Context, dir string, files []modelFile) error {
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(dir, f.Name)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() != f.Size {
			return fmt.Errorf("invalid model file: %s", f.Name)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, copyErr := io.Copy(h, &contextReader{ctx: ctx, reader: file})
		closeErr := file.Close()
		if err = errors.Join(copyErr, closeErr); err != nil {
			return err
		}
		if hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
			return fmt.Errorf("model checksum mismatch: %s", f.Name)
		}
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(b)
}

// Install blocks until completion. Call only for an explicit user download action.
// StartInstall owns the reservation so a cancelled worker cannot claim a retry.
func (m *Manager) Install(ctx context.Context, id string) error {
	result, err := m.StartInstall(ctx, id)
	if err != nil {
		return err
	}
	return <-result
}

func (m *Manager) installBody(ctx context.Context, generation uint64) (err error) {
	if err = m.installEngine(ctx); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(m.cfg.RootDir, ".download-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(stage)) }()
	client := &http.Client{Timeout: 2 * time.Hour, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		host := req.URL.Hostname()
		trusted := host == "huggingface.co" || strings.HasSuffix(host, ".huggingface.co") || host == "hf.co" || strings.HasSuffix(host, ".hf.co")
		if req.URL.Scheme != "https" || !trusted || len(via) > 10 {
			return errors.New("unsafe model download redirect")
		}
		return nil
	}}
	m.mu.Lock()
	if generation != m.installGeneration {
		m.mu.Unlock()
		return context.Canceled
	}
	m.status.Phase = "weights"
	m.mu.Unlock()
	for _, f := range modelFiles {
		if err = downloadFile(ctx, client, stage, f, func(n int64) {
			m.mu.Lock()
			if generation == m.installGeneration {
				m.status.DownloadedBytes += n
			}
			m.mu.Unlock()
		}); err != nil {
			return err
		}
	}
	m.mu.Lock()
	if generation != m.installGeneration {
		m.mu.Unlock()
		return context.Canceled
	}
	m.status.State = "verifying"
	m.mu.Unlock()
	if err = verifyFiles(ctx, stage, modelFiles); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(stage, "installed.json"), []byte(`{"revision":"`+Revision+`","engine_version":"`+EngineVersion+`"}`), 0600); err != nil {
		return err
	}
	if err = os.RemoveAll(m.modelDir()); err != nil {
		return err
	}
	return os.Rename(stage, m.modelDir())
}
