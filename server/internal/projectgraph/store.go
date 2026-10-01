package projectgraph

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Event struct {
	At        time.Time
	Type      string
	ProjectID string
	NodeID    string
	Data      map[string]any
}

func (e Event) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"at": e.At.UTC(), "type": e.Type, "project_id": e.ProjectID, "node_id": e.NodeID, "data": e.Data})
}

func (e *Event) UnmarshalJSON(raw []byte) error {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return err
	}
	if err := json.Unmarshal(values["at"], &e.At); err != nil {
		return err
	}
	if err := json.Unmarshal(values["type"], &e.Type); err != nil {
		return err
	}
	if err := json.Unmarshal(values["project_id"], &e.ProjectID); err != nil {
		return err
	}
	_ = json.Unmarshal(values["node_id"], &e.NodeID)
	_ = json.Unmarshal(values["data"], &e.Data)
	return nil
}

type Store struct {
	path string
	mu   sync.Mutex
}

var stores sync.Map // canonical events.jsonl path -> *Store

func Open(root, projectID string) (*Store, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, errors.New("project graph: project id is required")
	}
	if projectID == "." || projectID == ".." || len(projectID) > 128 || filepath.Base(projectID) != projectID || filepath.Clean(projectID) != projectID {
		return nil, errors.New("project graph: project id is invalid")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("project graph: resolve root: %w", err)
	}
	dir := filepath.Join(root, ".project-graphs", projectID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("project graph: create store: %w", err)
	}
	path := filepath.Join(dir, "events.jsonl")
	store := &Store{path: path}
	actual, _ := stores.LoadOrStore(path, store)
	return actual.(*Store), nil
}

func (s *Store) Append(event Event) error {
	if s == nil || event.ProjectID == "" || event.Type == "" {
		return errors.New("project graph: invalid event")
	}
	event.At = event.At.UTC()
	raw, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("project graph: encode event: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("project graph: open event store: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("project graph: append event: %w", err)
	}
	return nil
}

func (s *Store) Events() ([]Event, error) {
	if s == nil {
		return nil, errors.New("project graph: nil store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return []Event{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("project graph: open event store: %w", err)
	}
	defer file.Close()
	var events []Event
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("project graph: decode event: %w", err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("project graph: read events: %w", err)
	}
	return events, nil
}

func HashSummary(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
