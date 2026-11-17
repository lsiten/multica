package workspacefiles

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type operationReceipt struct {
	Hash    string                                `json:"hash"`
	State   string                                `json:"state"`
	Failure string                                `json:"failure,omitempty"`
	Request Request                               `json:"request"`
	Result  protocol.WorkspaceFileOperationResult `json:"result"`
	Edit    *protocol.WorkspaceFileEditResult     `json:"edit,omitempty"`
}

func receiptKey(commandID string) string {
	hash := sha256.Sum256([]byte(commandID))
	return hex.EncodeToString(hash[:])
}

func receiptIdentity(request Request) (string, string, error) {
	// Local IPC and HTTP use different actor labels for the same owner. Keep the
	// first actor for attribution without changing the operation's replay identity.
	data, err := json.Marshal(request.Operation)
	if err != nil {
		return "", "", err
	}
	if request.OperationID != "" {
		// A server admission must precede the physical effect. An older receipt
		// cannot be adopted by a newly collected metadata snapshot on retry.
		data = append(append(data, 0), request.OperationID...)
	}
	hash := sha256.Sum256(data)
	return receiptKey(request.CommandID), hex.EncodeToString(hash[:]), nil
}

func loadReceipt(root *os.Root, key string) (operationReceipt, bool, error) {
	var receipt operationReceipt
	file, err := root.Open(key + ".json")
	if errors.Is(err, os.ErrNotExist) {
		return receipt, false, nil
	}
	if err != nil {
		return receipt, false, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 512<<10+1))
	if err != nil {
		return receipt, false, err
	}
	if len(data) > 512<<10 {
		return receipt, false, ErrOperationMismatch
	}
	if err := json.Unmarshal(data, &receipt); err != nil {
		return receipt, false, err
	}
	return receipt, true, nil
}

func saveReceipt(root *os.Root, key string, receipt operationReceipt) error {
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	temp := ".receipt-" + rand.Text()
	if err := writeBytes(root, temp, data); err != nil {
		return err
	}
	defer root.Remove(temp)
	return root.Rename(temp, key+".json")
}

func writeBytes(root *os.Root, name string, data []byte) error {
	file, err := root.OpenFile(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Sync(); err != nil {
		return errors.Join(err, file.Close())
	}
	return file.Close()
}
