package agentdesktop

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func downloadFile(ctx context.Context, client *http.Client, source, destination string, limit int64) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected HTTP status %s", response.Status)
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, io.LimitReader(response.Body, limit+1))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written > limit {
		return fmt.Errorf("download exceeds %d bytes", limit)
	}
	return nil
}

func verifyFileSHA256(path, expected string) error {
	actual, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("SHA-256 mismatch: got %s, want %s", actual, expected)
	}
	return nil
}

func extractRelease(archivePath, destination string, asset releaseAsset) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	allowed := make(map[string]bool, len(asset.Helpers)+1)
	allowed[binaryName()] = true
	for _, helper := range asset.Helpers {
		allowed[helper] = true
	}
	reader := tar.NewReader(gzipReader)
	var extracted int64
	found := make(map[string]bool, len(allowed))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := filepath.Base(header.Name)
		if filepath.Clean(header.Name) != name || !allowed[name] || header.Typeflag != tar.TypeReg {
			return fmt.Errorf("archive contains unexpected entry %q", header.Name)
		}
		if found[name] {
			return fmt.Errorf("archive repeats %s", name)
		}
		if header.Size < 0 || extracted+header.Size > maxExtractedBytes {
			return fmt.Errorf("archive exceeds %d extracted bytes", maxExtractedBytes)
		}
		extracted += header.Size
		target := filepath.Join(destination, name)
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(output, reader, header.Size)
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		found[name] = true
	}
	for name := range allowed {
		if !found[name] {
			return fmt.Errorf("archive is missing %s", name)
		}
	}
	return nil
}

func writeChecksumManifest(directory string, asset releaseAsset) error {
	names := append([]string{binaryName(), "VERSION", "LICENSE"}, asset.Helpers...)
	sort.Strings(names)
	var manifest strings.Builder
	for _, name := range names {
		sum, err := fileSHA256(filepath.Join(directory, name))
		if err != nil {
			return fmt.Errorf("checksum %s: %w", name, err)
		}
		fmt.Fprintf(&manifest, "%s  %s\n", sum, name)
	}
	if err := os.WriteFile(filepath.Join(directory, "SHA256SUMS"), []byte(manifest.String()), 0o644); err != nil {
		return fmt.Errorf("write agent-desktop checksums: %w", err)
	}
	return nil
}

func replaceDirectory(target, staging string) error {
	backup := target + ".previous"
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("clear previous agent-desktop backup: %w", err)
	}
	targetExists := false
	if _, err := os.Stat(target); err == nil {
		targetExists = true
		if err := os.Rename(target, backup); err != nil {
			return fmt.Errorf("move previous agent-desktop install: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect agent-desktop install: %w", err)
	}
	if err := os.Rename(staging, target); err != nil {
		if targetExists {
			_ = os.Rename(backup, target)
		}
		return fmt.Errorf("activate agent-desktop install: %w", err)
	}
	if targetExists {
		if err := os.RemoveAll(backup); err != nil {
			return fmt.Errorf("remove previous agent-desktop install: %w", err)
		}
	}
	return nil
}
