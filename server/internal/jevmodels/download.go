package jevmodels

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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

func downloadFile(ctx context.Context, client *http.Client, target string, model Model, f modelFile, update func(int64)) (err error) {
	u := "https://huggingface.co/" + model.ID + "/resolve/" + model.Revision + "/" + f.Name
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
	gitHash := sha1.New()
	fmt.Fprintf(gitHash, "blob %d\x00", f.Size)
	n, err := io.Copy(progressWriter{writer: io.MultiWriter(file, h, gitHash), update: update}, io.LimitReader(res.Body, f.Size+1))
	if err != nil {
		return err
	}
	validHash := f.SHA256 != "" && hex.EncodeToString(h.Sum(nil)) == f.SHA256 || f.SHA256 == "" && f.GitSHA1 != "" && hex.EncodeToString(gitHash.Sum(nil)) == f.GitSHA1
	if n != f.Size || !validHash {
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
		gitHash := sha1.New()
		fmt.Fprintf(gitHash, "blob %d\x00", f.Size)
		_, copyErr := io.Copy(io.MultiWriter(h, gitHash), &contextReader{ctx: ctx, reader: file})
		closeErr := file.Close()
		if err = errors.Join(copyErr, closeErr); err != nil {
			return err
		}
		validHash := f.SHA256 != "" && hex.EncodeToString(h.Sum(nil)) == f.SHA256 || f.SHA256 == "" && f.GitSHA1 != "" && hex.EncodeToString(gitHash.Sum(nil)) == f.GitSHA1
		if !validHash {
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
func (m *Manager) Install(ctx context.Context, id string, revisions ...string) error {
	result, err := m.StartInstall(ctx, id, revisions...)
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
	client := hubClient(2 * time.Hour)
	m.mu.Lock()
	if generation != m.installGeneration {
		m.mu.Unlock()
		return context.Canceled
	}
	m.status.Phase = "weights"
	m.mu.Unlock()
	for _, f := range m.files {
		if err = downloadFile(ctx, client, stage, m.model, f, func(n int64) {
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
	persistErr := m.persistInstallLocked("")
	m.mu.Unlock()
	if persistErr != nil {
		return persistErr
	}
	if err = verifyFiles(ctx, stage, m.files); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(stage, "installed.json"), []byte(`{"revision":"`+m.model.Revision+`","engine_version":"`+EngineVersion+`"}`), 0600); err != nil {
		return err
	}
	if err = os.RemoveAll(m.modelDir()); err != nil {
		return err
	}
	return os.Rename(stage, m.modelDir())
}
