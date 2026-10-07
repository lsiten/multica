// Package applicationgateway carries bounded application streams independently of daemon control traffic.
package applicationgateway

import (
	"io"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Stream adapts binary WebSocket messages to a byte stream for HTTP and TCP forwarding.
type Stream struct {
	connection *websocket.Conn
	reader     io.Reader
	readMu     sync.Mutex
	writeMu    sync.Mutex
	closeOnce  sync.Once
	onClose    func()
}

// NewStream preserves backpressure by writing bounded frames without an unbounded queue.
func NewStream(connection *websocket.Conn, onClose func()) *Stream {
	connection.SetReadLimit(64 << 10)
	return &Stream{connection: connection, onClose: onClose}
}

func (s *Stream) Read(buffer []byte) (int, error) {
	s.readMu.Lock()
	defer s.readMu.Unlock()
	for {
		if s.reader == nil {
			kind, reader, err := s.connection.NextReader()
			if err != nil {
				return 0, err
			}
			if kind != websocket.BinaryMessage {
				continue
			}
			s.reader = reader
		}
		count, err := s.reader.Read(buffer)
		if err == io.EOF {
			s.reader = nil
			if count > 0 {
				return count, nil
			}
			continue
		}
		return count, err
	}
}

func (s *Stream) Write(buffer []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	count := 0
	for len(buffer) > 0 {
		amount := min(len(buffer), 32<<10)
		if err := s.connection.WriteMessage(websocket.BinaryMessage, buffer[:amount]); err != nil {
			return count, err
		}
		count += amount
		buffer = buffer[amount:]
	}
	return count, nil
}

func (s *Stream) Close() error {
	var err error
	s.closeOnce.Do(func() {
		err = s.connection.Close()
		if s.onClose != nil {
			s.onClose()
		}
	})
	return err
}
func (s *Stream) LocalAddr() net.Addr  { return s.connection.LocalAddr() }
func (s *Stream) RemoteAddr() net.Addr { return s.connection.RemoteAddr() }
func (s *Stream) SetDeadline(deadline time.Time) error {
	if err := s.SetReadDeadline(deadline); err != nil {
		return err
	}
	return s.SetWriteDeadline(deadline)
}
func (s *Stream) SetReadDeadline(deadline time.Time) error {
	return s.connection.SetReadDeadline(deadline)
}
func (s *Stream) SetWriteDeadline(deadline time.Time) error {
	return s.connection.SetWriteDeadline(deadline)
}

// Bridge forwards until either direction closes, then drains both owned goroutines.
func Bridge(first, second net.Conn) error {
	done := make(chan error, 2)
	go func() { _, err := io.Copy(first, second); done <- err }()
	go func() { _, err := io.Copy(second, first); done <- err }()
	err := <-done
	first.Close()
	second.Close()
	<-done
	return err
}
