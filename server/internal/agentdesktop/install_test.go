package agentdesktop

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseAssetsPinAllSupportedPlatforms(t *testing.T) {
	t.Parallel()
	for _, key := range []string{
		"darwin/arm64",
		"darwin/amd64",
		"linux/arm64",
		"linux/amd64",
		"windows/arm64",
		"windows/amd64",
	} {
		asset, ok := releaseAssets[key]
		if !ok {
			t.Fatalf("%s is not pinned", key)
		}
		if !strings.Contains(asset.Archive, Version) || len(asset.ArchiveSHA) != 64 {
			t.Fatalf("%s has incomplete release metadata: %+v", key, asset)
		}
	}
}

func TestInstallRejectsArchiveChecksumMismatch(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not the pinned archive"))
	}))
	defer server.Close()
	home := t.TempDir()

	path, err := Install(context.Background(), InstallOptions{
		Home:           home,
		ReleaseBaseURL: server.URL,
		HTTPClient:     server.Client(),
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("Install() = %q, %v; want checksum rejection", path, err)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".multica", "tools", "agent-desktop", Version)); !os.IsNotExist(statErr) {
		t.Fatalf("unverified archive was installed: %v", statErr)
	}
}

func TestInstallReusesVerifiedManagedRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell executable")
	}
	home := t.TempDir()
	target := filepath.Join(home, ".multica", "tools", "agent-desktop", Version)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(target, binaryName())
	script := "#!/bin/sh\nprintf '%s\\n' '{\"ok\":true,\"data\":{\"version\":\"" + Version + "\",\"os\":\"darwin\",\"target\":\"fixture\"}}'\n"
	if err := os.WriteFile(executable, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"VERSION": Tag + "\n", "LICENSE": "Apache License fixture\n"}
	for _, helper := range PlatformHelpers() {
		files[helper] = "helper fixture\n"
		if err := os.WriteFile(filepath.Join(target, helper), []byte(files[helper]), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range files {
		if name == "VERSION" || name == "LICENSE" {
			if err := os.WriteFile(filepath.Join(target, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	names := append([]string{binaryName(), "VERSION", "LICENSE"}, PlatformHelpers()...)
	var manifest strings.Builder
	for _, name := range names {
		sum, err := fileSHA256(filepath.Join(target, name))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&manifest, "%s  %s\n", sum, name)
	}
	if err := os.WriteFile(filepath.Join(target, "SHA256SUMS"), []byte(manifest.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		http.Error(w, "network must not be used", http.StatusInternalServerError)
	}))
	defer server.Close()

	path, err := Install(context.Background(), InstallOptions{
		Home:           home,
		ReleaseBaseURL: server.URL,
		HTTPClient:     server.Client(),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if path != target || requests != 0 {
		t.Fatalf("Install() path=%q requests=%d, want verified reuse without network", path, requests)
	}
}

func TestExtractReleaseRejectsUnexpectedEntries(t *testing.T) {
	t.Parallel()
	archive := filepath.Join(t.TempDir(), "release.tar.gz")
	writeTarGzip(t, archive, map[string]string{"../agent-desktop": "payload"})

	err := extractRelease(archive, t.TempDir(), releaseAsset{})
	if err == nil || !strings.Contains(err.Error(), "unexpected entry") {
		t.Fatalf("extractRelease() error = %v, want unsafe entry rejection", err)
	}
}

func TestExtractReleaseRequiresPlatformHelper(t *testing.T) {
	t.Parallel()
	archive := filepath.Join(t.TempDir(), "release.tar.gz")
	writeTarGzip(t, archive, map[string]string{binaryName(): "payload"})

	err := extractRelease(archive, t.TempDir(), releaseAsset{Helpers: []string{"required-helper"}})
	if err == nil || !strings.Contains(err.Error(), "missing required-helper") {
		t.Fatalf("extractRelease() error = %v, want missing helper rejection", err)
	}
}

func writeTarGzip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, content := range files {
		header := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(tarWriter, bytes.NewBufferString(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
