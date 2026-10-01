package communications

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Each key owns an exclusive reservation file and an immutable receipt.
// Crashed or incomplete reservations fail closed without stale lock recovery.
type FileIdempotencyStore struct{ root string }

func NewFileIdempotencyStore(path string) (*FileIdempotencyStore, error) {
	if path == "" {
		return nil, ErrInvalidRequest
	}
	root := path + ".d"
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, fmt.Errorf("create communication receipts: %w", err)
	}
	return &FileIdempotencyStore{root: root}, nil
}
func (s *FileIdempotencyStore) filename(key, suffix string) string {
	digest := sha256.Sum256([]byte(key))
	return filepath.Join(s.root, hex.EncodeToString(digest[:])+suffix)
}
func (s *FileIdempotencyStore) Reserve(key string) (bool, error) {
	return s.ReserveFingerprint(key, "")
}
func (s *FileIdempotencyStore) ReserveFingerprint(key, fingerprint string) (bool, error) {
	f, err := os.OpenFile(s.filename(key, ".pending"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		saved, readErr := os.ReadFile(s.filename(key, ".pending"))
		if readErr != nil || len(saved) == 0 {
			return false, ErrAmbiguousOperation
		}
		var reserved string
		if json.Unmarshal(saved, &reserved) != nil {
			return false, ErrAmbiguousOperation
		}
		if reserved != fingerprint {
			return false, ErrIdempotencyConflict
		}
		_, found, pending := s.Get(key)
		if !found || pending {
			return false, ErrAmbiguousOperation
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reserve communication operation: %w", err)
	}
	data, err := json.Marshal(fingerprint)
	if err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return false, fmt.Errorf("persist communication reservation: %w", err)
	}
	return true, nil
}
func (s *FileIdempotencyStore) Complete(key string, call Call) error {
	data, err := json.Marshal(call)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.root, "receipt-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	// No other request can own this reservation while the pending file exists.
	return os.Rename(name, s.filename(key, ".receipt"))
}
func (s *FileIdempotencyStore) Forget(key string) error {
	return os.Remove(s.filename(key, ".pending"))
}
func (s *FileIdempotencyStore) Get(key string) (Call, bool, bool) {
	f, err := os.Open(s.filename(key, ".receipt"))
	if err != nil {
		return Call{}, true, true
	}
	defer f.Close()
	var call Call
	if json.NewDecoder(io.LimitReader(f, 65536)).Decode(&call) != nil || call.SID == "" {
		return Call{}, true, true
	}
	return call, true, false
}
