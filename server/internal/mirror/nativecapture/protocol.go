package nativecapture

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const MaxMessageBytes = 65536

type Request struct {
	Version   int                  `json:"version"`
	Build     string               `json:"build"`
	Token     []byte               `json:"token,omitempty"`
	Media     bool                 `json:"media,omitempty"`
	ID        string               `json:"id"`
	Operation string               `json:"operation"`
	Resource  protocol.ResourceKey `json:"resource"`
	Epoch     Epoch                `json:"epoch"`
	Capture   *CaptureOptions      `json:"capture,omitempty"`
}

type Response struct {
	Version int                `json:"version"`
	Build   string             `json:"build"`
	ID      string             `json:"id"`
	Error   string             `json:"error,omitempty"`
	Epoch   Epoch              `json:"epoch"`
	Sources []SourceDescriptor `json:"sources,omitempty"`
	Capture *CaptureDescriptor `json:"capture,omitempty"`
}

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
