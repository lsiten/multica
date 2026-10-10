package daemon

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/applicationgateway"
	"github.com/multica-ai/multica/server/internal/daemon/applicationhost"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (c *Client) applicationTunnelURL(runtimeID, kind, daemonID string) (string, error) {
	address, err := url.Parse(c.baseURL)
	if err != nil {
		return "", err
	}
	if address.Scheme == "https" {
		address.Scheme = "wss"
	} else if address.Scheme == "http" {
		address.Scheme = "ws"
	} else {
		return "", errors.New("invalid application tunnel server URL")
	}
	address.Path = "/api/daemon/runtimes/" + runtimeID + "/applications/tunnel/" + kind
	address.RawQuery = url.Values{"daemon_id": {daemonID}}.Encode()
	return address.String(), nil
}

func (c *Client) connectApplicationTunnel(ctx context.Context, runtimeID, kind, daemonID string) (*websocket.Conn, error) {
	address, err := c.applicationTunnelURL(runtimeID, kind, daemonID)
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	if c.token != "" {
		headers.Set("Authorization", "Bearer "+c.token)
	}
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	connection, response, err := dialer.DialContext(ctx, address, headers)
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	return connection, err
}

func (d *Daemon) applicationTunnelLoop(ctx context.Context) {
	var workers sync.WaitGroup
	defer workers.Wait()
	active := map[string]context.CancelFunc{}
	defer func() {
		for _, cancel := range active {
			cancel()
		}
	}()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		current := map[string]bool{}
		for _, runtimeID := range d.allRuntimeIDs() {
			if _, supported := d.applicationServerCapabilities.Load(runtimeID); !supported {
				continue
			}
			current[runtimeID] = true
			if _, exists := active[runtimeID]; exists {
				continue
			}
			connectionCtx, cancel := context.WithCancel(ctx)
			active[runtimeID] = cancel
			workers.Go(func() { d.serveApplicationTunnel(connectionCtx, runtimeID) })
		}
		for runtimeID, cancel := range active {
			if !current[runtimeID] {
				cancel()
				delete(active, runtimeID)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *Daemon) serveApplicationTunnel(ctx context.Context, runtimeID string) {
	for attempt := 0; ctx.Err() == nil; attempt++ {
		connection, err := d.applicationTransportFor(runtimeID).Tunnel(ctx, "control")
		if err == nil {
			err = d.readApplicationTunnel(ctx, runtimeID, connection)
			connection.Close()
		}
		if ctx.Err() != nil {
			return
		}
		d.logger.Debug("application data channel reconnecting", "runtime_id", runtimeID)
		timer := time.NewTimer(time.Duration(min(30, 1<<min(attempt, 5))) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (d *Daemon) readApplicationTunnel(ctx context.Context, runtimeID string, connection *websocket.Conn) error {
	channelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	closed := make(chan struct{})
	go func() {
		select {
		case <-channelCtx.Done():
			connection.Close()
		case <-closed:
		}
	}()
	defer close(closed)
	var workers sync.WaitGroup
	defer workers.Wait()
	semaphore := make(chan struct{}, 128)
	connection.SetReadLimit(64 << 10)
	for {
		var request protocol.ApplicationTunnelRequest
		if err := connection.ReadJSON(&request); err != nil {
			cancel()
			return err
		}
		if request.RuntimeID != runtimeID || len(request.StreamID) != 64 || len(request.Token) != 64 {
			continue
		}
		if _, err := hex.DecodeString(request.StreamID + request.Token); err != nil {
			continue
		}
		select {
		case semaphore <- struct{}{}:
		default:
			continue
		}
		workers.Go(func() {
			defer func() { <-semaphore }()
			if err := d.forwardApplicationTunnel(channelCtx, request); err != nil && channelCtx.Err() == nil {
				d.logger.Debug("application stream unavailable", "instance_id", request.InstanceID)
			}
		})
	}
}

func (d *Daemon) forwardApplicationTunnel(ctx context.Context, request protocol.ApplicationTunnelRequest) error {
	if !d.environmentRuntimeOwnedHere(environmentOperationScope{WorkspaceID: request.WorkspaceID, RuntimeID: request.RuntimeID}) {
		return errors.New("application tunnel runtime ownership changed")
	}
	value, exists := d.applicationCommands.Load(request.InstanceID)
	if !exists {
		return errors.New("application instance is not registered locally")
	}
	command := value.(protocol.ApplicationControlCommand)
	if command.WorkspaceID != request.WorkspaceID || command.RuntimeID != request.RuntimeID || command.Generation != request.Generation || request.Kind == "service" && command.Action == "stop" {
		return errors.New("application tunnel instance scope changed")
	}
	if request.Kind != "service" && request.Kind != "logs" {
		return errors.New("unsupported application tunnel kind")
	}
	if request.Kind == "service" && (request.Port < 1 || request.Port != command.Config.Port) {
		return errors.New("application tunnel port is not authorized")
	}
	if request.Kind == "logs" && (request.Limit < 1 || request.Limit > 65536 || len(request.Cursor) > 512) {
		return errors.New("invalid application log request")
	}
	data, err := d.applicationTransportFor(request.RuntimeID).Tunnel(ctx, "data")
	if err != nil {
		return err
	}
	defer data.Close()
	if err = data.WriteJSON(map[string]string{"stream_id": request.StreamID, "token": request.Token}); err != nil {
		return err
	}
	stream := applicationgateway.NewStream(data, nil)
	if request.Kind == "logs" {
		if command.Config.Mode != "managed" {
			return errors.New("external services do not have managed process logs")
		}
		page, err := d.readApplicationLogs(ctx, command, request.Cursor, request.Limit)
		if err != nil {
			return err
		}
		return json.NewEncoder(stream).Encode(page)
	}
	local, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(request.Port)))
	if err != nil {
		return err
	}
	defer local.Close()
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			local.Close()
			stream.Close()
		case <-done:
		}
	}()
	defer close(done)
	return applicationgateway.Bridge(stream, local)
}

func (d *Daemon) readApplicationLogs(ctx context.Context, command protocol.ApplicationControlCommand, cursor string, limit int) (applicationhost.LogPage, error) {
	path, record, err := d.applicationRecord(command)
	if err != nil {
		return applicationhost.LogPage{}, err
	}
	if record.Command.Generation > command.Generation {
		return applicationhost.LogPage{}, errors.New("local application has a newer generation")
	}
	client, err := applicationhost.NewClient(record)
	if err == nil {
		page, readErr := client.Logs(ctx, cursor, limit)
		if readErr == nil {
			return page, nil
		}
	}
	if ctx.Err() != nil {
		return applicationhost.LogPage{}, ctx.Err()
	}
	return applicationhost.ReadStoredLogs(path, cursor, limit)
}
