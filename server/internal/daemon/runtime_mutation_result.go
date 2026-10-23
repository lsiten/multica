package daemon

import (
	"encoding/json"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

// consumedRuntimeResultError distinguishes an unusable completed result from a
// transport failure whose execution outcome still needs to be observed.
type consumedRuntimeResultError struct{ cause error }

func (e *consumedRuntimeResultError) Error() string { return e.cause.Error() }
func (e *consumedRuntimeResultError) Unwrap() error { return e.cause }

func decodeCompletedRuntimeResult(receipt *runtimeproc.Receipt, out any) error {
	if receipt.Error != nil {
		return receipt.Error
	}
	if out != nil {
		if err := json.Unmarshal(receipt.Result, out); err != nil {
			return &consumedRuntimeResultError{cause: err}
		}
	}
	return nil
}
