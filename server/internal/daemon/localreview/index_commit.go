package localreview

import (
	"context"
	"errors"
	"strings"
)

var ErrNothingStaged = errors.New("no staged changes to commit")
var ErrInvalidCommitRequest = errors.New("valid branch, index, message and recovery preparation are required")

type IndexCommitRequest struct {
	Path, Branch, Head, IndexID, Message string
}

type PreparedIndexCommit struct {
	Commit  string `json:"commit"`
	Parent  string `json:"parent"`
	Tree    string `json:"tree"`
	Branch  string `json:"branch"`
	IndexID string `json:"index_id"`
}

// CommitIndexPrepared commits the staged tree only. It never runs hooks, signs,
// stages working changes or resets the checkout. The durable prepare callback
// must succeed before the selected branch is updated using its exact old value.
func CommitIndexPrepared(ctx context.Context, request IndexCommitRequest, prepare func(PreparedIndexCommit) error) (commit string, err error) {
	if prepare == nil || strings.TrimSpace(request.Message) == "" || len(request.Message) > 8000 || strings.ContainsRune(request.Message, 0) || !validBlobID(request.IndexID) || !validGitObjectID(request.Head) {
		return "", ErrInvalidCommitRequest
	}
	tx, err := openIndexTransaction(ctx, request.Path)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, tx.Close()) }()
	if tx.original != request.IndexID || tx.branch != request.Branch || tx.head != request.Head {
		return "", ErrIndexStateChanged
	}
	tree, err := tx.git(ctx, nil, "write-tree")
	if err != nil {
		return "", err
	}
	if !validGitObjectID(tree) {
		return "", ErrInvalidCommitRequest
	}
	previousTree, err := tx.git(ctx, nil, "rev-parse", tx.head+"^{tree}")
	if err != nil {
		return "", err
	}
	if tree == previousTree {
		return "", ErrNothingStaged
	}
	commit, err = tx.git(ctx, strings.NewReader(request.Message+"\n"), "commit-tree", tree, "-p", tx.head, "-F", "-")
	if err != nil {
		return "", err
	}
	if !validGitObjectID(commit) {
		return "", ErrInvalidCommitRequest
	}
	if err := tx.validate(ctx); err != nil {
		return "", err
	}
	if err := prepare(PreparedIndexCommit{Commit: commit, Parent: tx.head, Tree: tree, Branch: tx.branch, IndexID: tx.original}); err != nil {
		return "", err
	}
	if err := tx.validate(ctx); err != nil {
		return "", err
	}
	// Address the selected branch explicitly, never a possibly redirected HEAD.
	// Git atomically compares its old value while taking its reference lock.
	if _, err := tx.git(ctx, nil, "update-ref", "--no-deref", "refs/heads/"+tx.branch, commit, tx.head); err != nil {
		return "", err
	}
	return commit, nil
}
