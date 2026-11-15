package nativecapture

import (
	"encoding/binary"
	"encoding/hex"
	"io"
)

const (
	MediaHeaderBytes     = 120
	MaxMediaPayloadBytes = 8 * 1024 * 1024
)

type MediaKind uint8

const (
	MediaVideo MediaKind = iota
	MediaTerminal
)

type TerminalReason string

const (
	TerminalClosed             TerminalReason = "closed"
	TerminalSourceGone         TerminalReason = "source_gone"
	TerminalPermissionDenied   TerminalReason = "permission_denied"
	TerminalCaptureUnavailable TerminalReason = "capture_unavailable"
)

type MediaSample struct {
	StreamID       string
	Kind           MediaKind
	TerminalReason TerminalReason
	Epoch          Epoch
	DisplayID      uint32
	PTSNanos       int64
	DurationNanos  int64
	KeyFrame       bool
	AnnexB         []byte
}

func (r TerminalReason) valid() bool {
	switch r {
	case TerminalClosed, TerminalSourceGone, TerminalPermissionDenied, TerminalCaptureUnavailable:
		return true
	default:
		return false
	}
}

func WriteMediaSample(w io.Writer, sample MediaSample) error {
	var flags uint16
	payload := sample.AnnexB
	switch sample.Kind {
	case MediaVideo:
		if len(payload) == 0 || len(payload) > MaxMediaPayloadBytes || sample.PTSNanos < 0 || sample.DurationNanos <= 0 || sample.TerminalReason != "" {
			return ErrProtocol
		}
		if sample.KeyFrame {
			flags = 1
		}
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
	copy(header, "MMIR")
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
	if err := writeBytes(w, header); err != nil {
		return err
	}
	return writeBytes(w, payload)
}

func ReadMediaSample(r io.Reader) (MediaSample, error) {
	var header [MediaHeaderBytes]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return MediaSample{}, err
	}
	flags := binary.BigEndian.Uint16(header[6:])
	length := binary.BigEndian.Uint32(header[8:])
	if string(header[:4]) != "MMIR" || binary.BigEndian.Uint16(header[4:]) != 1 || (flags != 0 && flags != 1 && flags != 2) || length == 0 || length > MaxMediaPayloadBytes {
		return MediaSample{}, ErrProtocol
	}
	sample := MediaSample{
		StreamID:      hex.EncodeToString(header[40:56]),
		Epoch:         Epoch{NativeEpoch: hex.EncodeToString(header[56:88]), DisplayGeneration: hex.EncodeToString(header[88:120]), GeometryRevision: binary.BigEndian.Uint64(header[28:])},
		DisplayID:     binary.BigEndian.Uint32(header[36:]),
		PTSNanos:      int64(binary.BigEndian.Uint64(header[12:])),
		DurationNanos: int64(binary.BigEndian.Uint64(header[20:])),
		KeyFrame:      flags&1 != 0,
	}
	if sample.DisplayID == 0 || sample.Epoch.Validate() != nil {
		return MediaSample{}, ErrProtocol
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return MediaSample{}, err
	}
	if flags == 2 {
		if sample.PTSNanos != 0 || sample.DurationNanos != 0 || length > 32 {
			return MediaSample{}, ErrProtocol
		}
		sample.Kind = MediaTerminal
		sample.TerminalReason = TerminalReason(body)
		if !sample.TerminalReason.valid() {
			return MediaSample{}, ErrProtocol
		}
		return sample, nil
	}
	if sample.PTSNanos < 0 || sample.DurationNanos <= 0 {
		return MediaSample{}, ErrProtocol
	}
	sample.AnnexB = body
	return sample, nil
}

func writeBytes(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
