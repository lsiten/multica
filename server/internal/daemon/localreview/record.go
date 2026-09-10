package localreview

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"time"
)

// Record persists decisions outside the checkout, so review metadata never enters a diff.
type Record struct {
	IndexOperation  *IndexOperationReceipt `json:"index_operation,omitempty"`
	SnapshotID      string                 `json:"snapshot_id"`
	VersionID       string                 `json:"version_id,omitempty"`
	State           string                 `json:"state"`
	Comment         string                 `json:"comment"`
	MergedCommit    string                 `json:"merged_commit"`
	SourceHead      string                 `json:"source_head"`
	PreparedCommit  string                 `json:"prepared_commit,omitempty"`
	PreparedRequest *Event                 `json:"prepared_request,omitempty"`
	CommandID       string                 `json:"command_id,omitempty"`
	Snapshot        *Snapshot              `json:"snapshot,omitempty"`
	Events          []Event                `json:"events,omitempty"`
}

// Event preserves decisions on the owning runtime even after the diff changes.
type Event struct {
	VersionID  string    `json:"version_id,omitempty"`
	CommandID  string    `json:"command_id,omitempty"`
	Kind       string    `json:"kind"`
	SnapshotID string    `json:"snapshot_id"`
	Comment    string    `json:"comment"`
	ActorID    string    `json:"actor_id,omitempty"`
	ActorName  string    `json:"actor_name,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// RecordView excludes the private recovery journal and its full diff snapshot.
// The wire response already carries one snapshot at the top level.
type RecordView struct {
	SnapshotID   string  `json:"snapshot_id"`
	State        string  `json:"state"`
	Comment      string  `json:"comment"`
	MergedCommit string  `json:"merged_commit"`
	Events       []Event `json:"events,omitempty"`
}

func (r Record) View() RecordView {
	return RecordView{SnapshotID: r.SnapshotID, State: r.State, Comment: r.Comment, MergedCommit: r.MergedCommit, Events: r.Events}
}

func RecordKey(s Snapshot) string {
	hash := sha256.Sum256([]byte(s.Path + "\x00" + s.Target))
	return hex.EncodeToString(hash[:])
}

func LoadRecord(root, id string) (Record, error) {
	r := Record{SnapshotID: id, State: "draft"}
	if len(id) != 64 {
		return r, errors.New("invalid review snapshot")
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return r, errors.New("invalid snapshot identifier")
		}
	}
	b, err := readRecordBytes(root, ".local-review-"+id+".json")
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(b, &r)
	return r, err
}

func SaveRecord(root, key string, r Record) error {
	if _, err := LoadRecord(root, key); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if len(data) > maxRecordBytes {
		return ErrInvalidReviewVersion
	}
	directory, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer directory.Close()
	name := ".local-review-tmp-" + rand.Text()
	f, err := directory.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer directory.Remove(name)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return directory.Rename(name, ".local-review-"+key+".json")
}
