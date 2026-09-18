package native

import (
	"encoding/binary"
	"encoding/hex"
	"io"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// MediaHeaderBytes is the fixed v1 header length on inherited FD 5.
const MediaHeaderBytes = 120
const SnapshotChunkBytes = 64 * 1024
const MaxMediaPayloadBytes = 8 * 1024 * 1024

// MediaKind distinguishes encoded video from a terminal delivery event.
type MediaKind uint8

const (
	MediaVideo MediaKind = iota
	MediaTerminal
	MediaSnapshot
)

// TerminalReason is a safe fixed delivery reason, never native exception text.
type TerminalReason string

const (
	TerminalClosed             TerminalReason = "closed"
	TerminalSourceGone         TerminalReason = "source_gone"
	TerminalPermissionDenied   TerminalReason = "permission_denied"
	TerminalCaptureUnavailable TerminalReason = "capture_unavailable"
)

func (r TerminalReason) valid() bool {
	switch r {
	case TerminalClosed, TerminalSourceGone, TerminalPermissionDenied, TerminalCaptureUnavailable:
		return true
	default:
		return false
	}
}

// MediaSample binds an owned Annex-B access unit to one immutable capture session.
// StreamID is 16 random bytes encoded as 32 hex characters; epochs are 32-byte hex values.
type MediaSample struct {
	StreamID                      string
	Kind                          MediaKind
	TerminalReason                TerminalReason
	Epoch                         protocol.VscreenEpoch
	DisplayID                     uint32
	PTSNanos                      int64
	DurationNanos                 int64
	KeyFrame                      bool
	SnapshotOffset, SnapshotTotal uint32
	PNG                           []byte
	AnnexB                        []byte
}

// WriteMediaSample emits one fixed header and one payload. Serialize writers per media channel.
func WriteMediaSample(writer io.Writer, sample MediaSample) error {
	if sample.Kind != MediaSnapshot && (len(sample.PNG) != 0 || sample.SnapshotOffset != 0 || sample.SnapshotTotal != 0) {
		return ErrProtocol
	}
	payload := sample.AnnexB
	var flags uint16
	switch sample.Kind {
	case MediaVideo:
		if len(payload) == 0 || len(payload) > MaxMediaPayloadBytes || sample.PTSNanos < 0 || sample.DurationNanos <= 0 || sample.TerminalReason != "" {
			return ErrProtocol
		}
		if sample.KeyFrame {
			flags = 1
		}
	case MediaSnapshot:
		if len(sample.PNG) == 0 || len(sample.PNG) > SnapshotChunkBytes || sample.SnapshotTotal == 0 || sample.SnapshotTotal > MaxMediaPayloadBytes || uint64(sample.SnapshotOffset)+uint64(len(sample.PNG)) > uint64(sample.SnapshotTotal) || len(payload) != 0 || sample.KeyFrame || sample.TerminalReason != "" || sample.PTSNanos != 0 || sample.DurationNanos != 0 {
			return ErrProtocol
		}
		payload = sample.PNG
		flags = 4
		sample.PTSNanos = int64(sample.SnapshotOffset)
		sample.DurationNanos = int64(sample.SnapshotTotal)
	case MediaTerminal:
		if !sample.TerminalReason.valid() || len(payload) != 0 || sample.KeyFrame || sample.PTSNanos != 0 || sample.DurationNanos != 0 {
			return ErrProtocol
		}
		payload = []byte(sample.TerminalReason)
		flags = 2
	default:
		return ErrProtocol
	}
	if sample.DisplayID == 0 || sample.Epoch.Validate() != nil {
		return ErrProtocol
	}
	header := make([]byte, MediaHeaderBytes)
	copy(header, "MVSC")
	binary.BigEndian.PutUint16(header[4:], 1)
	binary.BigEndian.PutUint16(header[6:], flags)
	binary.BigEndian.PutUint32(header[8:], uint32(len(payload)))
	binary.BigEndian.PutUint64(header[12:], uint64(sample.PTSNanos))
	binary.BigEndian.PutUint64(header[20:], uint64(sample.DurationNanos))
	binary.BigEndian.PutUint64(header[28:], sample.Epoch.GeometryRevision)
	binary.BigEndian.PutUint32(header[36:], sample.DisplayID)
	for _, part := range []struct {
		value      string
		start, end int
	}{{sample.StreamID, 40, 56}, {sample.Epoch.NativeEpoch, 56, 88}, {sample.Epoch.DisplayGeneration, 88, 120}} {
		if len(part.value) != (part.end-part.start)*2 {
			return ErrProtocol
		}
		if _, err := hex.Decode(header[part.start:part.end], []byte(part.value)); err != nil {
			return ErrProtocol
		}
	}
	if err := writeMediaBytes(writer, header); err != nil {
		return err
	}
	return writeMediaBytes(writer, payload)
}

// ReadMediaSample bounds payload allocation before reading bytes and rejects unknown flags/version.
func ReadMediaSample(reader io.Reader) (MediaSample, error) {
	var header [MediaHeaderBytes]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return MediaSample{}, err
	}
	flags := binary.BigEndian.Uint16(header[6:])
	length := binary.BigEndian.Uint32(header[8:])
	if string(header[:4]) != "MVSC" || binary.BigEndian.Uint16(header[4:]) != 1 || (flags > 2 && flags != 4) || length == 0 || length > MaxMediaPayloadBytes {
		return MediaSample{}, ErrProtocol
	}
	sample := MediaSample{StreamID: hex.EncodeToString(header[40:56]), Epoch: protocol.VscreenEpoch{NativeEpoch: hex.EncodeToString(header[56:88]), DisplayGeneration: hex.EncodeToString(header[88:120]), GeometryRevision: binary.BigEndian.Uint64(header[28:])}, DisplayID: binary.BigEndian.Uint32(header[36:]), PTSNanos: int64(binary.BigEndian.Uint64(header[12:])), DurationNanos: int64(binary.BigEndian.Uint64(header[20:])), KeyFrame: flags&1 != 0}
	if sample.DisplayID == 0 || sample.Epoch.Validate() != nil {
		return MediaSample{}, ErrProtocol
	}
	if flags == 4 {
		if length > SnapshotChunkBytes || sample.PTSNanos < 0 || sample.DurationNanos <= 0 || sample.DurationNanos > MaxMediaPayloadBytes || sample.PTSNanos+int64(length) > sample.DurationNanos {
			return MediaSample{}, ErrProtocol
		}
		sample.Kind = MediaSnapshot
		sample.SnapshotOffset = uint32(sample.PTSNanos)
		sample.SnapshotTotal = uint32(sample.DurationNanos)
		sample.PTSNanos = 0
		sample.DurationNanos = 0
		sample.PNG = make([]byte, length)
		if _, err := io.ReadFull(reader, sample.PNG); err != nil {
			return MediaSample{}, err
		}
	} else if flags == 2 {
		if length > 32 || sample.PTSNanos != 0 || sample.DurationNanos != 0 {
			return MediaSample{}, ErrProtocol
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(reader, body); err != nil {
			return MediaSample{}, err
		}
		sample.Kind = MediaTerminal
		sample.TerminalReason = TerminalReason(body)
		if !sample.TerminalReason.valid() {
			return MediaSample{}, ErrProtocol
		}
	} else {
		if sample.PTSNanos < 0 || sample.DurationNanos <= 0 {
			return MediaSample{}, ErrProtocol
		}
		sample.AnnexB = make([]byte, length)
		if _, err := io.ReadFull(reader, sample.AnnexB); err != nil {
			return MediaSample{}, err
		}
	}
	return sample, nil
}

func writeMediaBytes(writer io.Writer, bytes []byte) error {
	for len(bytes) > 0 {
		n, err := writer.Write(bytes)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		bytes = bytes[n:]
	}
	return nil
}
