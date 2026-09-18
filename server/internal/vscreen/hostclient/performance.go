package hostclient

import (
	"context"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
)

// OwnedPID identifies only the child process supervised by this client.
func (c *Client) OwnedPID() int {
	if c.cmd == nil || c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}

// CaptureStatus reads native telemetry for this exact owned stream.
func (s *Stream) CaptureStatus(ctx context.Context) (*native.CaptureDescriptor, error) {
	response, err := s.client.Call(ctx, s.request("capture_status"))
	if err != nil {
		return nil, err
	}
	if response.Capture == nil {
		return nil, native.ErrProtocol
	}
	return response.Capture, nil
}
