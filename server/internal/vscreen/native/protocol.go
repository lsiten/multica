// Package native owns the private, same-binary virtual display host protocol.
package native

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const ProtocolVersion = 1
const MaxMessageBytes = 65536

var ErrProtocol = errors.New("invalid native host protocol")
var ErrUnsupported = errors.New("native virtual display unsupported")
var ErrUnavailable = errors.New("native display unavailable")

// Request is only accepted over the inherited authenticated parent socket.
type Request struct {
	Version    int                   `json:"version"`
	Build      string                `json:"build"`
	Token      []byte                `json:"token,omitempty"`
	AppControl bool                  `json:"app_control,omitempty"`
	App        *AppRequest           `json:"app,omitempty"`
	Media      bool                  `json:"media,omitempty"`
	Capture    *CaptureOptions       `json:"capture,omitempty"`
	ID         string                `json:"id"`
	Operation  string                `json:"operation"`
	Resource   protocol.ResourceKey  `json:"resource"`
	Epoch      protocol.VscreenEpoch `json:"epoch"`
	Width      uint32                `json:"width,omitempty"`
	Height     uint32                `json:"height,omitempty"`
}

// Display contains system readback, never a simulated desktop.
type Display struct {
	ID              uint32  `json:"display_id"`
	UUID            string  `json:"uuid"`
	Name            string  `json:"name"`
	Builtin         bool    `json:"builtin"`
	Managed         bool    `json:"managed"`
	LogicalWidth    float64 `json:"logical_width"`
	LogicalHeight   float64 `json:"logical_height"`
	Scale           float64 `json:"scale"`
	Main            bool    `json:"main"`
	MirrorOf        uint32  `json:"mirror_of"`
	X               int32   `json:"x"`
	Y               int32   `json:"y"`
	Width           uint32  `json:"width"`
	Height          uint32  `json:"height"`
	ScreenRecording bool    `json:"screen_recording"`
	CaptureVisible  bool    `json:"capture_visible"`
}

// Response binds every successful resource operation to the current host epoch.
type Response struct {
	Version   int                   `json:"version"`
	Build     string                `json:"build"`
	ID        string                `json:"id"`
	Error     string                `json:"error,omitempty"`
	Epoch     protocol.VscreenEpoch `json:"epoch"`
	Display   *Display              `json:"display,omitempty"`
	Displays  []Display             `json:"displays,omitempty"`
	Sources   []SourceDescriptor    `json:"sources,omitempty"`
	Capture   *CaptureDescriptor    `json:"capture,omitempty"`
	App       *AppResponse          `json:"app,omitempty"`
	Quiescent bool                  `json:"quiescent"`
}

// ReadMessage decodes one length-bounded frame and rejects unknown fields.
func ReadMessage[T Request | Response](r io.Reader, target *T) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > MaxMessageBytes {
		return ErrProtocol
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(r, body); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	if decoder.Decode(new(json.RawMessage)) != io.EOF {
		return ErrProtocol
	}
	return nil
}

// WriteMessage emits one bounded frame; callers serialize concurrent writes.
func WriteMessage[T Request | Response](w io.Writer, value T) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(body) > MaxMessageBytes {
		return ErrProtocol
	}
	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame, uint32(len(body)))
	copy(frame[4:], body)
	for len(frame) > 0 {
		n, err := w.Write(frame)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}
