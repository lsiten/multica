package execenv

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const worktreeBindingFile = ".worktree-binding.json"

// WorktreeConsumer identifies an execution claim, not a cached server status.
type WorktreeConsumer struct {
	TaskID       string `json:"task_id"`
	AgentID      string `json:"agent_id"`
	RuntimeID    string `json:"runtime_id"`
	DispatchedAt string `json:"dispatched_at,omitempty"`
}

// WorktreeBinding records the consumers of one physical, daemon-owned code root.
// Business retention is always reconciled against the server, never this file.
type WorktreeBinding struct {
	Owner     EnvRootOwner       `json:"owner"`
	Revision  uint64             `json:"revision"`
	Consumers []WorktreeConsumer `json:"consumers"`
}

// ReadWorktreeBinding validates physical ownership before exposing consumers.
func ReadWorktreeBinding(path string) (*WorktreeBinding, error) {
	file := filepath.Join(path, worktreeBindingFile)
	info, err := os.Lstat(file)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 128<<10 {
		return nil, errors.New("invalid worktree binding file")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var binding WorktreeBinding
	if err := json.Unmarshal(data, &binding); err != nil {
		return nil, err
	}
	owner, err := ReadEnvRootOwner(path)
	if err != nil || *owner != binding.Owner {
		return nil, errors.New("worktree binding owner changed")
	}
	if len(binding.Consumers) > 500 {
		return nil, errors.New("too many worktree consumers")
	}
	seen := map[string]bool{}
	for _, consumer := range binding.Consumers {
		if consumer.TaskID == "" || consumer.AgentID == "" || consumer.RuntimeID == "" || seen[consumer.TaskID] {
			return nil, errors.New("invalid worktree consumer")
		}
		seen[consumer.TaskID] = true
	}
	return &binding, nil
}

// UpdateWorktreeConsumer runs under the caller's execution claim and binding
// mutex. Replayed claims are idempotent; an old release cannot remove a new claim.
func UpdateWorktreeConsumer(path string, consumer WorktreeConsumer, release bool) error {
	binding, err := ReadWorktreeBinding(path)
	if errors.Is(err, os.ErrNotExist) {
		owner, ownerErr := ReadEnvRootOwner(path)
		if ownerErr != nil {
			return ownerErr
		}
		binding = &WorktreeBinding{Owner: *owner, Consumers: []WorktreeConsumer{}}
	} else if err != nil {
		return err
	}
	if consumer.TaskID == "" || consumer.AgentID == "" || consumer.RuntimeID == "" {
		return errors.New("missing worktree consumer identity")
	}
	updated := false
	for index, previous := range binding.Consumers {
		if previous.TaskID != consumer.TaskID {
			continue
		}
		if release {
			if previous != consumer {
				return nil
			}
			binding.Consumers = append(binding.Consumers[:index], binding.Consumers[index+1:]...)
		} else {
			if previous == consumer {
				return nil
			}
			binding.Consumers[index] = consumer
		}
		updated = true
		break
	}
	if !updated {
		if release {
			return nil
		}
		if len(binding.Consumers) >= 500 {
			return errors.New("too many worktree consumers")
		}
		binding.Consumers = append(binding.Consumers, consumer)
	}
	binding.Revision++
	data, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(path, worktreeBindingFile), data, 0600)
}
