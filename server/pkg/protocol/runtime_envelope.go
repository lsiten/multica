package protocol

import "fmt"

// RuntimeEnvelope binds a request/response pair to one daemon connection and
// runtime. It carries no capability by itself.
type RuntimeEnvelope struct {
	WorkspaceID      string `json:"workspace_id"`
	RuntimeID        string `json:"runtime_id"`
	DaemonGeneration string `json:"daemon_generation"`
	RequestID        string `json:"request_id"`
}

func (e RuntimeEnvelope) Validate() error {
	if !mirrorIdentity(e.WorkspaceID) || !mirrorIdentity(e.RuntimeID) ||
		!mirrorIdentity(e.DaemonGeneration) || !mirrorIdentity(e.RequestID) {
		return fmt.Errorf("%w: runtime envelope is incomplete", ErrInvalidMirrorContract)
	}
	return nil
}
