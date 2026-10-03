package jevmodels

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDownloadVerifiesExactRevisionAndHash(t *testing.T) {
	content := "curated model"
	hash := sha256.Sum256([]byte(content))
	f := modelFile{Name: "model.safetensors", Size: int64(len(content)), SHA256: hex.EncodeToString(hash[:])}
	for _, body := range []string{content, "wrong weights", content + "extra"} {
		t.Run(body, func(t *testing.T) {
			dir := t.TempDir()
			var progress int64
			client := &http.Client{Transport: fakeTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != "https://huggingface.co/"+ModelID+"/resolve/"+Revision+"/model.safetensors" {
					t.Fatal(r.URL)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			err := downloadFile(t.Context(), client, dir, Catalog()[0], f, func(n int64) { progress += n })
			if body == content {
				if err != nil {
					t.Fatal(err)
				}
				if progress != f.Size {
					t.Fatal(progress)
				}
				if err = verifyFiles(t.Context(), dir, []modelFile{f}); err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("corrupt model accepted")
			}
		})
	}
}

func TestVerifyRejectsSymlinksAndCancellation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "real"), []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "model")); err != nil {
		t.Skip(err)
	}
	h := sha256.Sum256([]byte("abc"))
	f := modelFile{Name: "model", Size: 3, SHA256: hex.EncodeToString(h[:])}
	if err := verifyFiles(t.Context(), dir, []modelFile{f}); err == nil {
		t.Fatal("symlink accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := verifyFiles(ctx, dir, []modelFile{f}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
