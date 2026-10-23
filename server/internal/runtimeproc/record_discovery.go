package runtimeproc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// DiscoverRecords returns private owner records under a daemon-owned root.
// The directory hash is only a locator; every record is still validated against
// its embedded immutable identity and private storage before being returned.
type DiscoveredRecord struct {
	Record Record
	Root   string
}

func DiscoverRecordLocations(root string) ([]DiscoveredRecord, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result := []DiscoveredRecord{}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(root, entry.Name(), "owner.json")
		if _, statErr := os.Lstat(path); errors.Is(statErr, os.ErrNotExist) {
			children, readErr := os.ReadDir(filepath.Join(root, entry.Name()))
			if readErr != nil {
				return result, readErr
			}
			for _, child := range children {
				if child.IsDir() && child.Type()&os.ModeSymlink == 0 {
					candidate := filepath.Join(root, entry.Name(), child.Name(), "owner.json")
					if _, err := os.Lstat(candidate); err == nil {
						path = candidate
						break
					}
				}
			}
		}
		raw, readErr := readPrivate(path)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return result, readErr
		}
		var record Record
		if json.Unmarshal(raw, &record) != nil {
			continue
		}
		if err := record.Identity.Validate(); err != nil {
			continue
		}
		scopeHash := sha256.Sum256(mustJSON(record.Identity.Scope))
		if filepath.Base(filepath.Dir(path)) != hex.EncodeToString(scopeHash[:]) {
			continue
		}
		if err := (Bootstrap{Root: root, Identity: record.Identity, Token: record.Token, Fence: record.Fence}).validate(); err != nil {
			continue
		}
		if record.Address != "" {
			if err := validateOrigin(record.Address); err != nil {
				continue
			}
		}
		result = append(result, DiscoveredRecord{Record: record, Root: filepath.Dir(filepath.Dir(path))})
		if len(result) > 256 {
			return result, errors.New("runtime record discovery quota exceeded")
		}
	}
	return result, nil
}

func DiscoverRecords(root string) ([]Record, error) {
	locations, err := DiscoverRecordLocations(root)
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(locations))
	for _, location := range locations {
		out = append(out, location.Record)
	}
	return out, nil
}
