package nativecapture

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

var ErrClosed = errors.New("native capture client closed")

type RemoteError struct{ Code string }

func (e *RemoteError) Error() string { return "native capture: " + e.Code }

type Config struct {
	Executable      string
	Build           string
	StartupTimeout  time.Duration
	CallTimeout     time.Duration
	ShutdownTimeout time.Duration
}

type Client struct {
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

func (c *Client) Call(ctx context.Context, request Request) (Response, error) {
	switch request.Operation {
	case "sources", "start_capture", "stop_capture", "force_keyframe", "capture_status":
	default:
		return Response{}, ErrProtocol
	}
	if request.Resource.Validate() != nil {
		return Response{}, ErrProtocol
	}
	if request.Operation != "sources" {
		if request.Epoch.Validate() != nil || request.Epoch.NativeEpoch != c.epoch {
			return Response{}, ErrProtocol
		}
	}
	request.Token = nil
	request.Media = false
	return c.exchange(ctx, request)
}

func (c *Client) exchange(ctx context.Context, request Request) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	select {
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case <-c.closed:
		return Response{}, ErrClosed
	case <-c.gate:
	}
	defer func() { c.gate <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	c.nextID++
	request.ID, request.Version, request.Build = strconv.FormatUint(c.nextID, 10), ProtocolVersion, c.build
	deadline, _ := ctx.Deadline()
	if err := c.conn.SetDeadline(deadline); err != nil {
		c.abort()
		return Response{}, ErrClosed
	}
	cancellationDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { c.abort(); close(cancellationDone) })
	defer func() {
		if !stop() {
			<-cancellationDone
		}
	}()
	var response Response
	err := WriteMessage(c.conn, request)
	if err == nil {
		err = ReadMessage(c.conn, &response)
	}
	if err != nil {
		c.abort()
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		return Response{}, fmt.Errorf("native capture exchange: %w", ErrProtocol)
	}
	if ctx.Err() != nil {
		c.abort()
		return Response{}, ctx.Err()
	}
	if response.ID != request.ID || response.Version != ProtocolVersion || response.Build != c.build ||
		response.Epoch.NativeEpoch == "" || c.epoch != "" && response.Epoch.NativeEpoch != c.epoch {
		c.abort()
		return Response{}, ErrProtocol
	}
	if response.Error != "" {
		return Response{}, remoteError(response.Error)
	}
	if request.Operation == "sources" || request.Operation == "start_capture" ||
		request.Operation == "stop_capture" || request.Operation == "force_keyframe" ||
		request.Operation == "capture_status" {
		if err := c.validateMediaResponse(request, response); err != nil {
			c.abort()
			return Response{}, err
		}
		return response, nil
	}
	return response, nil
}

func remoteError(code string) error {
	switch code {
	case "media_unavailable", "capture_conflict", "capture_limit", "stale_stream",
		"stale_epoch", "resource_invalid", "operation_unsupported":
		return &RemoteError{Code: code}
	default:
		return &RemoteError{Code: "unavailable"}
	}
}

func (c *Client) abort() {
	c.closeOnce.Do(func() {
		close(c.closed)
		_ = c.conn.Close()
		if c.media != nil {
			_ = c.media.Close()
			c.failMedia(ErrClosed)
		}
	})
}

func (c *Client) Close() error {
	c.stopOnce.Do(func() {
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
				c.stopErr = fmt.Errorf("kill native capture: %w", err)
			}
		}
		timer.Reset(c.shutdownTimeout)
		select {
		case <-c.exited:
		case <-timer.C:
			c.stopErr = errors.Join(c.stopErr, errors.New("native capture reap timed out"))
		}
	})
	return c.stopErr
}

func (c *Client) Done() <-chan struct{} { return c.closed }

func sourceEpoch(source SourceDescriptor) Epoch {
	return Epoch{NativeEpoch: source.NativeEpoch, DisplayGeneration: source.Generation, GeometryRevision: source.GeometryRevision}
}

func (c *Client) validSource(source SourceDescriptor, resource protocol.ResourceKey) bool {
	return source.Resource == resource && source.Source.Validate() == nil &&
		sourceEpoch(source).Validate() == nil && source.NativeEpoch == c.epoch &&
		source.DisplayID != 0 && source.Width > 0 && source.Height > 0 &&
		source.LogicalWidth > 0 && source.LogicalHeight > 0 && source.Scale > 0
}
