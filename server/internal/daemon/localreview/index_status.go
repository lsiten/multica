package localreview

import (
	"context"
	"errors"
	"strings"
)

var ErrInvalidIndexStatus = errors.New("invalid Git index status")
var ErrIndexStateChanged = errors.New("Git branch changed while reading status; refresh before staging")

type IndexFile struct {
	Path        string `json:"path"`
	OldPath     string `json:"old_path,omitempty"`
	IndexCode   string `json:"index_code"`
	WorkingCode string `json:"working_code"`
	Staged      bool   `json:"staged"`
	Unstaged    bool   `json:"unstaged"`
	Untracked   bool   `json:"untracked"`
	Conflicted  bool   `json:"conflicted"`
	Unsupported bool   `json:"unsupported"`
}

type IndexStatus struct {
	Branch string      `json:"branch"`
	Head   string      `json:"head"`
	Files  []IndexFile `json:"files"`
}

// ReadIndexStatus reports HEAD/index/worktree state, not target-branch history.
// Optional Git locks are disabled by git(), so inspecting it cannot refresh the
// user's index. Mutation preconditions must separately pin index/content bytes.
func ReadIndexStatus(ctx context.Context, path string) (IndexStatus, error) {
	branch, err := trimmed(ctx, path, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return IndexStatus{}, err
	}
	head, err := trimmed(ctx, path, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return IndexStatus{}, err
	}
	raw, err := git(ctx, path, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--renames")
	if err != nil {
		return IndexStatus{}, err
	}
	files, err := parseIndexFiles(raw)
	if err != nil {
		return IndexStatus{}, err
	}
	currentBranch, err := trimmed(ctx, path, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return IndexStatus{}, err
	}
	currentHead, err := trimmed(ctx, path, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return IndexStatus{}, err
	}
	if currentBranch != branch || currentHead != head {
		return IndexStatus{}, ErrIndexStateChanged
	}
	return IndexStatus{Branch: branch, Head: head, Files: files}, nil
}

func parseIndexFiles(raw string) ([]IndexFile, error) {
	files := []IndexFile{}
	if raw == "" {
		return files, nil
	}
	if !strings.HasSuffix(raw, "\x00") {
		return nil, ErrInvalidIndexStatus
	}
	parts := strings.Split(strings.TrimSuffix(raw, "\x00"), "\x00")
	for index := 0; index < len(parts); index++ {
		entry := parts[index]
		if len(entry) < 4 || entry[2] != ' ' {
			return nil, ErrInvalidIndexStatus
		}
		name := strings.TrimSuffix(entry[3:], "/")
		if !validVersionPath(name) {
			return nil, ErrInvalidIndexStatus
		}
		x, y := entry[0], entry[1]
		if !strings.ContainsRune(" MADRCUT?!", rune(x)) || !strings.ContainsRune(" MADRCUT?!", rune(y)) {
			return nil, ErrInvalidIndexStatus
		}
		file := IndexFile{Path: name, IndexCode: string(x), WorkingCode: string(y), Unsupported: name != entry[3:]}
		file.Untracked = x == '?' && y == '?'
		file.Staged = x != ' ' && x != '?' && x != '!'
		file.Unstaged = y != ' ' && y != '!'
		file.Conflicted = x == 'U' || y == 'U' || entry[:2] == "AA" || entry[:2] == "DD"
		if x == 'R' || x == 'C' || y == 'R' || y == 'C' {
			index++
			if index >= len(parts) || !validVersionPath(parts[index]) {
				return nil, ErrInvalidIndexStatus
			}
			file.OldPath = parts[index]
		}
		files = append(files, file)
		if len(files) > maxVersionFiles {
			return nil, ErrInvalidIndexStatus
		}
	}
	return files, nil
}
