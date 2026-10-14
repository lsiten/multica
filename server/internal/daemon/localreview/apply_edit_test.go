package localreview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func sha256Of(t *testing.T, b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func writeRootFile(t *testing.T, root *os.Root, name, content string, mode os.FileMode) {
	t.Helper()
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func readRootFile(t *testing.T, root *os.Root, name string) string {
	t.Helper()
	f, err := root.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b := make([]byte, 1<<20)
	n, err := f.Read(b)
	if err != nil && err != os.ErrClosed {
		t.Fatal(err)
	}
	return string(b[:n])
}

func TestApplyWorkingFileEditSuccess(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	writeRootFile(t, root, "notes.txt", "hello", 0600)
	base := sha256Of(t, []byte("hello"))

	res, err := ApplyWorkingFileEdit(context.Background(), root, EditWorkingFileRequest{
		FilePath:   "notes.txt",
		BaseSHA256: base,
		NewContent: []byte("hello world"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readRootFile(t, root, "notes.txt"); got != "hello world" {
		t.Fatalf("content = %q, want %q", got, "hello world")
	}
	if res.PreviousSHA256 != base {
		t.Fatalf("previous = %s, want %s", res.PreviousSHA256, base)
	}
	if res.NewSHA256 != sha256Of(t, []byte("hello world")) {
		t.Fatalf("new digest mismatch")
	}
	if res.Bytes != int64(len("hello world")) {
		t.Fatalf("bytes = %d", res.Bytes)
	}
}

func TestApplyWorkingFileEditRejectsChangedBase(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	writeRootFile(t, root, "notes.txt", "hello", 0600)
	// The base the editor opened is stale; the file now differs.
	res, err := ApplyWorkingFileEdit(context.Background(), root, EditWorkingFileRequest{
		FilePath:   "notes.txt",
		BaseSHA256: sha256Of(t, []byte("stale base")),
		NewContent: []byte("hello world"),
	})
	if !errors.Is(err, ErrWorkingFileChanged) {
		t.Fatalf("err = %v, want ErrWorkingFileChanged", err)
	}
	if res.FilePath != "" {
		t.Fatalf("result should be empty on conflict")
	}
	if got := readRootFile(t, root, "notes.txt"); got != "hello" {
		t.Fatalf("content = %q, want unchanged", got)
	}
}

func TestApplyWorkingFileEditPreservesMode(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	writeRootFile(t, root, "script.sh", "echo hi", 0700)
	base := sha256Of(t, []byte("echo hi"))
	if _, err := ApplyWorkingFileEdit(context.Background(), root, EditWorkingFileRequest{
		FilePath:   "script.sh",
		BaseSHA256: base,
		NewContent: []byte("echo world"),
	}); err != nil {
		t.Fatal(err)
	}
	info, err := root.Lstat("script.sh")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("mode = %o, want 0700", info.Mode().Perm())
	}
}

func TestApplyWorkingFileEditRejectsNonRegular(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	writeRootFile(t, root, "real.txt", "hi", 0600)
	if err := os.Symlink("real.txt", filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyWorkingFileEdit(context.Background(), root, EditWorkingFileRequest{
		FilePath:   "link.txt",
		BaseSHA256: sha256Of(t, []byte("hi")),
		NewContent: []byte("changed"),
	}); !errors.Is(err, ErrWorkingFileChanged) {
		t.Fatalf("err = %v, want ErrWorkingFileChanged for non-regular", err)
	}
}

func TestApplyWorkingFileEditInvalidPath(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := ApplyWorkingFileEdit(context.Background(), root, EditWorkingFileRequest{
		FilePath:   "../escape.txt",
		NewContent: []byte("x"),
	}); err == nil {
		t.Fatal("expected error for invalid path")
	}
}
