package localreview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
)

var ErrIndexOperationMismatch = errors.New("index operation ID was used with different data")

type IndexOperationRequest struct {
	Path, CommandID, ActorID, Action, VersionID, IndexID, Head, Branch, Message string
	Paths                                                                       []string
}

type IndexOperationResult struct {
	IndexID string               `json:"index_id,omitempty"`
	Commit  *PreparedIndexCommit `json:"commit,omitempty"`
}

type IndexOperationReceipt struct {
	RequestHash string               `json:"request_hash"`
	State       string               `json:"state"`
	Result      IndexOperationResult `json:"result"`
}

func indexReceiptIdentity(request IndexOperationRequest) (string, string, error) {
	if request.Path == "" || request.CommandID == "" || len(request.CommandID) > 128 || request.ActorID == "" || !validBlobID(request.VersionID) || !validBlobID(request.IndexID) {
		return "", "", ErrInvalidIndexStatus
	}
	switch request.Action {
	case "stage", "unstage":
		if len(request.Paths) == 0 || len(request.Paths) > 1000 {
			return "", "", ErrInvalidIndexStatus
		}
	case "commit":
		if !validGitObjectID(request.Head) || request.Branch == "" || request.Message == "" {
			return "", "", ErrInvalidCommitRequest
		}
	default:
		return "", "", ErrInvalidIndexStatus
	}
	request.Paths = slices.Clone(request.Paths)
	slices.Sort(request.Paths)
	for index, path := range request.Paths {
		if !validVersionPath(path) || (index > 0 && request.Paths[index-1] == path) {
			return "", "", ErrInvalidIndexStatus
		}
	}
	data, err := json.Marshal(request)
	if err != nil {
		return "", "", err
	}
	hash := sha256.Sum256(data)
	key := sha256.Sum256([]byte("index-operation\x00" + request.Path + "\x00" + request.CommandID))
	return hex.EncodeToString(key[:]), hex.EncodeToString(hash[:]), nil
}

func LoadIndexReceipt(root string, request IndexOperationRequest) (IndexOperationReceipt, bool, error) {
	key, hash, err := indexReceiptIdentity(request)
	if err != nil {
		return IndexOperationReceipt{}, false, err
	}
	record, err := LoadRecord(root, key)
	if err != nil {
		return IndexOperationReceipt{}, false, err
	}
	if record.IndexOperation == nil {
		return IndexOperationReceipt{}, false, nil
	}
	receipt := *record.IndexOperation
	if receipt.RequestHash != hash || record.VersionID != request.VersionID || (receipt.State != "prepared" && receipt.State != "completed") {
		return IndexOperationReceipt{}, false, ErrIndexOperationMismatch
	}
	if err := validateIndexResult(request, receipt.Result); err != nil {
		return IndexOperationReceipt{}, false, err
	}
	return receipt, true, nil
}

func PrepareIndexReceipt(root string, request IndexOperationRequest, result IndexOperationResult) error {
	key, hash, err := indexReceiptIdentity(request)
	if err != nil {
		return err
	}
	if _, exists, err := LoadIndexReceipt(root, request); err != nil {
		return err
	} else if exists {
		return ErrIndexOperationMismatch
	}
	if err := validateIndexResult(request, result); err != nil {
		return err
	}
	return SaveRecord(root, key, Record{VersionID: request.VersionID, State: "index_operation", IndexOperation: &IndexOperationReceipt{RequestHash: hash, State: "prepared", Result: result}})
}

func validateIndexResult(request IndexOperationRequest, result IndexOperationResult) error {
	if request.Action == "commit" {
		if result.Commit == nil || !validGitObjectID(result.Commit.Commit) || !validGitObjectID(result.Commit.Tree) || result.Commit.Parent != request.Head || result.Commit.Branch != request.Branch || result.Commit.IndexID != request.IndexID {
			return ErrInvalidCommitRequest
		}
	} else if !validBlobID(result.IndexID) {
		return ErrInvalidIndexStatus
	}
	return nil
}

func CompleteIndexReceipt(root string, request IndexOperationRequest) error {
	receipt, exists, err := LoadIndexReceipt(root, request)
	if err != nil {
		return err
	}
	if !exists {
		return ErrIndexOperationMismatch
	}
	key, _, err := indexReceiptIdentity(request)
	if err != nil {
		return err
	}
	receipt.State = "completed"
	return SaveRecord(root, key, Record{VersionID: request.VersionID, State: "index_operation", IndexOperation: &receipt})
}
