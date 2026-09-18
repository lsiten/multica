// Package hostclient owns a private native host process and its serialized control stream.
package hostclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/native"
)

// ErrClosed means the stream is no longer usable; uncertain operations are never retried.
var ErrClosed = errors.New("native host client closed")

// RemoteError reports a bounded host error code without including host-controlled text.
type RemoteError struct{ Code string }

func (e *RemoteError) Error() string { return "native host: " + e.Code }

// Config supplies the caller-verified executable and version/commit build identity.
// Zero timeouts select bounded defaults. Executable must be an absolute file path.
type Config struct {
	AppControl      bool
	Media           bool
	Executable      string
	Build           string
	StartupTimeout  time.Duration
	CallTimeout     time.Duration
	ShutdownTimeout time.Duration
}

// Client owns exactly one helper. Call serializes control requests; Close is concurrent-safe.
type Client struct {
	apps            *appChannel
	snapshots       map[string]*pendingSnapshot
	snapshotUsed    map[string]bool
	media           net.Conn
	mediaMu         sync.Mutex
	streams         map[string]*Stream
	mediaErr        error
	mediaDone       chan struct{}
	closing         bool
	conn            net.Conn
	cmd             *exec.Cmd
	build           string
	epoch           string
	timeout         time.Duration
	shutdownTimeout time.Duration
	gate            chan struct{}
	closed          chan struct{}
	exited          chan struct{}
	closeOnce       sync.Once
	stopOnce        sync.Once
	stopErr         error
	nextID          uint64
}

// NativeEpoch identifies this helper incarnation, independent of display generations.
func (c *Client) NativeEpoch() string { return c.epoch }

// Call performs one operation. Cancelling an in-flight request permanently closes the
// stream because its mutation outcome may be unknown; cancelling a queued call does not.
func (c *Client) Call(ctx context.Context, request native.Request) (native.Response, error) {
	switch request.Operation {
	case "list", "ensure", "describe", "quiesce", "dispose", "sources", "start_capture", "stop_capture", "force_keyframe", "capture_status":
	default:
		return native.Response{}, native.ErrProtocol
	}
	if request.Operation != "list" {
		if err := request.Resource.Validate(); err != nil {
			return native.Response{}, err
		}
	}
	if request.Operation == "describe" || request.Operation == "quiesce" || request.Operation == "dispose" || isCaptureOperation(request.Operation) {
		if err := request.Epoch.Validate(); err != nil {
			return native.Response{}, err
		}
		if request.Epoch.NativeEpoch != c.epoch {
			return native.Response{}, native.ErrProtocol
		}
	}
	request.Token = nil
	request.Media = false
	return c.exchange(ctx, request)
}

func (c *Client) exchange(ctx context.Context, request native.Request) (native.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	select {
	case <-ctx.Done():
		return native.Response{}, ctx.Err()
	case <-c.closed:
		return native.Response{}, ErrClosed
	case <-c.gate:
	}
	defer func() { c.gate <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return native.Response{}, err
	}
	select {
	case <-c.closed:
		return native.Response{}, ErrClosed
	default:
	}
	c.nextID++
	request.ID, request.Version, request.Build = strconv.FormatUint(c.nextID, 10), native.ProtocolVersion, c.build
	deadline, _ := ctx.Deadline()
	if err := c.conn.SetDeadline(deadline); err != nil {
		c.abort()
		return native.Response{}, ErrClosed
	}
	cancellationDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { c.abort(); close(cancellationDone) })
	defer func() {
		if !stop() {
			<-cancellationDone
		}
	}()
	var response native.Response
	err := native.WriteMessage(c.conn, request)
	if err == nil {
		err = native.ReadMessage(c.conn, &response)
	}
	if err != nil {
		c.abort()
		if ctx.Err() != nil {
			return native.Response{}, ctx.Err()
		}
		// Decoder errors can contain hostile text. Preserve the classification only.
		return native.Response{}, fmt.Errorf("native host exchange: %w", native.ErrProtocol)
	}
	if ctx.Err() != nil {
		c.abort()
		return native.Response{}, ctx.Err()
	}
	if response.ID != request.ID || response.Version != native.ProtocolVersion || response.Build != c.build || response.Epoch.NativeEpoch == "" || c.epoch != "" && response.Epoch.NativeEpoch != c.epoch {
		c.abort()
		return native.Response{}, native.ErrProtocol
	}
	if response.Error != "" {
		return native.Response{}, remoteError(response.Error)
	}
	if request.Operation == "sources" || isCaptureOperation(request.Operation) {
		if err := c.validateMediaResponse(request, response); err != nil {
			c.abort()
			return native.Response{}, err
		}
		return response, nil
	}
	if request.Operation != "hello" && request.Operation != "list" {
		if err := response.Epoch.Validate(); err != nil {
			c.abort()
			return native.Response{}, native.ErrProtocol
		}
		if request.Operation != "ensure" && (response.Epoch.DisplayGeneration != request.Epoch.DisplayGeneration || response.Epoch.GeometryRevision < request.Epoch.GeometryRevision) {
			c.abort()
			return native.Response{}, native.ErrProtocol
		}
		if (request.Operation == "quiesce" || request.Operation == "dispose") && !response.Quiescent {
			c.abort()
			return native.Response{}, native.ErrProtocol
		}
		if request.Operation != "dispose" && response.Display == nil {
			c.abort()
			return native.Response{}, native.ErrProtocol
		}
	}
	return response, nil
}

func remoteError(code string) error {
	switch code {
	case "screen recording permission denied":
		return &RemoteError{Code: "screen_recording_denied"}
	case "media_unavailable", "capture_conflict", "capture_limit", "stale_stream":
		return &RemoteError{Code: code}
	case "resource_invalid", "geometry_invalid", "geometry_conflict", "resource_limit", "display_unavailable", "stale_epoch", "quiescence_required", "operation_unsupported":
		return &RemoteError{Code: code}
	default:
		return &RemoteError{Code: "unavailable"}
	}
}

func (c *Client) abort() {
	c.closeOnce.Do(func() {
		close(c.closed)
		c.failApps(ErrClosed)
		// Closing the socket is the host's cleanup signal, even after framing failure.
		_ = c.conn.Close()
		if c.media != nil {
			_ = c.media.Close()
			c.failMedia(ErrClosed)
		}
	})
}

// Close signals EOF, allows bounded cleanup, then kills only the owned helper if
// necessary. All callers wait for the same shutdown result, including process reaping.
func (c *Client) Close() error {
	c.stopOnce.Do(func() {
		c.failApps(ErrClosed)
		c.closeStreams()
		c.abort()
		if c.mediaDone != nil {
			<-c.mediaDone
		}
		timer := time.NewTimer(c.shutdownTimeout)
		defer timer.Stop()
		select {
		case <-c.exited:
			return
		case <-timer.C:
		}
		if err := c.cmd.Process.Kill(); err != nil {
			select {
			case <-c.exited:
				return
			default:
				c.stopErr = fmt.Errorf("kill native host: %w", err)
			}
		}
		timer.Reset(c.shutdownTimeout)
		select {
		case <-c.exited:
		case <-timer.C:
			c.stopErr = errors.Join(c.stopErr, errors.New("native host reap timed out"))
		}
	})
	return c.stopErr
}
