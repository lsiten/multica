package runtimeproc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Review artifact bounds. These are separate from the 256 KiB in-band cap and
// never change it. An oversized review result is written to a private anchored
// namespace and referenced in-band instead of being carried inline, so the
// runtimeproc control channel keeps its bound.
const (
	reviewArtifactMaxInput  = 512 << 10
	reviewArtifactMaxOutput = 12 << 20
)

// ReviewArtifactNamespace returns the anchored private directory for review
// artifacts. It is derived only from the caller-owned root; a record can never
// redirect it to another path.
func ReviewArtifactNamespace(root string) string {
	return filepath.Join(root, "review-artifacts")
}

// ReviewArtifactName binds an artifact to the exact request id, which already
// carries the replay epoch. Both the producing and reading services derive it
// identically from the request id, so a returned reference can never redirect a
// read to a different file.
func ReviewArtifactName(requestID string) string {
	sum := sha256.Sum256([]byte("review-artifact:" + requestID))
	return hex.EncodeToString(sum[:]) + ".json"
}

// ReviewArtifactReference is the small in-band pointer to a large result.
// It carries only the request id, hash and length for validation; the reader
// re-derives the file name from the request id and never trusts a path from
// the record.
type ReviewArtifactReference struct {
	RequestID string `json:"request_id"`
	Hash      string `json:"hash"`
	Length    int64  `json:"length"`
}

func validateReviewArtifactName(name string) error {
	if len(name) != 69 || name[64:] != ".json" {
		return errors.New("invalid review artifact name")
	}
	if _, err := hex.DecodeString(name[:64]); err != nil {
		return errors.New("invalid review artifact name")
	}
	return nil
}

// PrepareReviewArtifactNamespace creates the private artifact directory once.
// Existing storage must already satisfy the private owner/permission checks.
func PrepareReviewArtifactNamespace(root string) error {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return errors.New("invalid runtime root")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if errors.Is(err, os.ErrNotExist) {
		if err = os.MkdirAll(root, 0700); err != nil {
			return err
		}
		if err = secureNewDirectory(root); err != nil {
			return err
		}
		resolved, err = filepath.EvalSymlinks(root)
	}
	if err != nil {
		return err
	}
	if resolved != root {
		return errors.New("runtime root contains a symlink")
	}
	if err = checkDirectory(root); err != nil {
		return err
	}
	namespace := ReviewArtifactNamespace(root)
	if err = os.Mkdir(namespace, 0700); err == nil {
		err = secureNewDirectory(namespace)
	} else if errors.Is(err, os.ErrExist) {
		err = nil
	}
	if err != nil {
		return err
	}
	return checkDirectory(namespace)
}

// PublishReviewArtifact atomically writes a bounded review result to the
// private namespace. The body must already fit within the output cap and be
// valid JSON. A stale artifact with the same name is never overwritten.
func PublishReviewArtifact(namespace, name string, body []byte) error {
	if err := validateReviewArtifactName(name); err != nil {
		return err
	}
	if len(body) == 0 || len(body) > reviewArtifactMaxOutput {
		return errors.New("review artifact length out of bounds")
	}
	if !json.Valid(body) {
		return errors.New("review artifact is not valid json")
	}
	if err := checkDirectory(namespace); err != nil {
		return err
	}
	path := filepath.Join(namespace, name)
	if _, err := os.Lstat(path); err == nil {
		return errors.New("review artifact already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temp, err := os.CreateTemp(namespace, ".review-artifact-")
	if err != nil {
		return err
	}
	cleanup := func() {
		temp.Close()
		os.Remove(temp.Name())
	}
	if err = ProtectPrivateArtifact(temp); err != nil {
		cleanup()
		return err
	}
	if _, err = temp.Write(body); err != nil {
		cleanup()
		return err
	}
	if err = temp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err = temp.Close(); err != nil {
		os.Remove(temp.Name())
		return err
	}
	if err = ReplacePrivateArtifact(temp.Name(), path); err != nil {
		cleanup()
		return err
	}
	return SyncPrivateArtifactDirectory(namespace)
}

// ReadReviewArtifact reads a bounded review result, validating private
// ownership, mode, exact length and hash. A mismatch, an over-cap read or a
// file that changes during open is a failure, never a partial result.
func ReadReviewArtifact(namespace, name, expectedHash string, expectedLength int64) ([]byte, error) {
	if err := validateReviewArtifactName(name); err != nil {
		return nil, err
	}
	if expectedLength <= 0 || expectedLength > reviewArtifactMaxOutput {
		return nil, errors.New("review artifact length out of bounds")
	}
	if err := checkDirectory(namespace); err != nil {
		return nil, err
	}
	path := filepath.Join(namespace, name)
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("review artifact is not a regular file")
	}
	if before.Size() != expectedLength || before.Size() > reviewArtifactMaxOutput {
		return nil, errors.New("review artifact length mismatch")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		return nil, errors.New("review artifact changed during opening")
	}
	if err = ValidatePrivateArtifact(f); err != nil {
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(f, int64(reviewArtifactMaxOutput)+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) != expectedLength || int64(len(body)) > reviewArtifactMaxOutput {
		return nil, errors.New("review artifact length mismatch")
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != expectedHash || !json.Valid(body) {
		return nil, errors.New("review artifact hash mismatch")
	}
	return body, nil
}

// PruneReviewArtifacts removes private artifacts not in keep, bounded by count
// and total bytes. It removes the oldest first, only touches regular private
// files matching the anchored name pattern, never follows symlinks, and never
// trusts a path from a record. Returned is the number of artifacts removed.
func PruneReviewArtifacts(namespace string, keep map[string]bool, maxCount int, maxBytes int64) (int, error) {
	if err := checkDirectory(namespace); err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(namespace)
	if err != nil {
		return 0, err
	}
	type candidate struct {
		name string
		size int64
		mod  time.Time
	}
	var candidates []candidate
	var total int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if err := validateReviewArtifactName(entry.Name()); err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return 0, err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		total += info.Size()
		if !keep[entry.Name()] {
			candidates = append(candidates, candidate{name: entry.Name(), size: info.Size(), mod: info.ModTime()})
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].mod.Before(candidates[j].mod) })
	removed := 0
	for _, c := range candidates {
		if maxCount >= 0 && len(entries)-removed <= maxCount {
			break
		}
		if maxBytes > 0 && total <= maxBytes {
			break
		}
		if err := removePrivateArtifact(namespace, c.name); err != nil {
			return removed, err
		}
		removed++
		total -= c.size
	}
	return removed, nil
}

// removePrivateArtifact deletes a private regular file by re-checking it is not
// a symlink before unlinking. It refuses to remove a non-regular entry.
func removePrivateArtifact(namespace, name string) error {
	if err := validateReviewArtifactName(name); err != nil {
		return err
	}
	path := filepath.Join(namespace, name)
	before, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return errors.New("refusing to remove a non-regular runtime artifact")
	}
	return os.Remove(path)
}
