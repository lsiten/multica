package hostclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type appChannel struct {
	conn    net.Conn
	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  uint64
	pending map[string]chan native.Response
	err     error
	done    chan struct{}
}

func (c *Client) readApps() {
	a := c.apps
	defer close(a.done)
	for {
		var r native.Response
		if err := native.ReadMessage(a.conn, &r); err != nil {
			c.failApps(ErrClosed)
			return
		}
		a.mu.Lock()
		ch := a.pending[r.ID]
		id, e := strconv.ParseUint(r.ID, 10, 64)
		valid := e == nil && id > 0 && id <= a.nextID && r.Version == native.ProtocolVersion && r.Build == c.build && (r.Epoch.NativeEpoch == c.epoch || c.epoch == "")
		a.mu.Unlock()
		if !valid {
			c.failApps(native.ErrProtocol)
			return
		}
		if ch != nil {
			select {
			case ch <- r:
			default:
				c.failApps(native.ErrProtocol)
				return
			}
		}
	}
}
func (c *Client) failApps(err error) {
	if c.apps == nil {
		return
	}
	a := c.apps
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return
	}
	a.err = err
	a.conn.Close()
	for id, ch := range a.pending {
		select {
		case ch <- native.Response{ID: id, Error: "app_channel_closed"}:
		default:
		}
	}
}
func (c *Client) appExchange(ctx context.Context, operation string, a appcontrol.Authority, p native.AppRequest) (*native.AppResponse, error) {
	if c.apps == nil {
		return nil, ErrClosed
	}
	channel := c.apps
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.Authority = a
	channel.writeMu.Lock()
	channel.mu.Lock()
	if channel.err != nil || len(channel.pending) >= 16 {
		channel.mu.Unlock()
		channel.writeMu.Unlock()
		return nil, ErrClosed
	}
	channel.nextID++
	id := strconv.FormatUint(channel.nextID, 10)
	reply := make(chan native.Response, 1)
	channel.pending[id] = reply
	channel.mu.Unlock()
	defer func() { channel.mu.Lock(); delete(channel.pending, id); channel.mu.Unlock() }()
	r := native.Request{Version: native.ProtocolVersion, Build: c.build, ID: id, Operation: operation, Resource: a.Resource, Epoch: a.Epoch, App: &p}
	deadline, _ := ctx.Deadline()
	err := channel.conn.SetWriteDeadline(deadline)
	if err == nil {
		err = native.WriteMessage(channel.conn, r)
	}
	channel.writeMu.Unlock()
	if err != nil {
		c.failApps(native.ErrProtocol)
		return nil, err
	}
	select {
	case response := <-reply:
		if err := ctx.Err(); err != nil {
			c.cancelApp(a, id)
			return nil, err
		}
		if response.Error != "" {
			return nil, &RemoteError{Code: safeAppCode(response.Error)}
		}
		if operation != "app_probe" && response.Epoch != a.Epoch {
			c.failApps(native.ErrProtocol)
			return nil, native.ErrProtocol
		}
		return response.App, nil
	case <-ctx.Done():
		c.cancelApp(a, id)
		return nil, ctx.Err()
	}
}
func (c *Client) cancelApp(a appcontrol.Authority, id string) {
	channel := c.apps
	// Only the app channel receives cancellation; FD3 and video streams survive.
	// The fixed write deadline bounds a non-reading helper without killing it.
	channel.writeMu.Lock()
	channel.mu.Lock()
	channel.nextID++
	cancelID := strconv.FormatUint(channel.nextID, 10)
	channel.mu.Unlock()
	cancelRequest := native.Request{Version: native.ProtocolVersion, Build: c.build, ID: cancelID, Operation: "app_cancel", Resource: a.Resource, Epoch: a.Epoch, App: &native.AppRequest{Authority: a, CancelID: id}}
	err := channel.conn.SetWriteDeadline(time.Now().Add(time.Second))
	if err == nil {
		err = native.WriteMessage(channel.conn, cancelRequest)
	}
	channel.writeMu.Unlock()
	if err != nil {
		c.failApps(native.ErrProtocol)
	}
}

func safeAppCode(code string) string {
	switch code {
	case "unsupported_platform", "stale_authority", "stale_window", "stale_snapshot", "needs_intervention", "action_uncertain", "action_conflict", "invalid_action", "invalid_launch", "app_claim_conflict", "process_changed", "authority_required", "human_grant_required", "accessibility_denied", "screen_recording_denied", "quiescence_required", "app_limit", "cancelled", "deadline_exceeded", "app_channel_closed", "app_unavailable":
		return code
	default:
		return "app_unavailable"
	}
}
func ttlMillis(ttl time.Duration) (uint32, error) {
	if ttl < time.Millisecond || ttl > 15*time.Second {
		return 0, native.ErrProtocol
	}
	return uint32(ttl / time.Millisecond), nil
}
func (c *Client) appGrant(ctx context.Context, op string, a appcontrol.Authority, ttl time.Duration) error {
	ms, err := ttlMillis(ttl)
	if err != nil {
		return err
	}
	_, err = c.appExchange(ctx, op, a, native.AppRequest{LeaseTTLMS: ms})
	return err
}

// Grant installs a fresh daemon-verified task lease after quiescence. Call ResumeApps explicitly.
func (c *Client) Grant(ctx context.Context, a appcontrol.Authority, ttl time.Duration) error {
	return c.appGrant(ctx, "app_grant", a, ttl)
}

// Renew extends only the current unexpired lease, capped at fifteen seconds.
func (c *Client) Renew(ctx context.Context, a appcontrol.Authority, ttl time.Duration) error {
	return c.appGrant(ctx, "app_renew", a, ttl)
}

// GrantObserver installs separate read-only authority; it cannot authorize input or takeover.
func (c *Client) GrantObserver(ctx context.Context, a appcontrol.Authority, ttl time.Duration) error {
	return c.appGrant(ctx, "app_observer_grant", a, ttl)
}

// Revoke fences input immediately and waits for native quiescence.
func (c *Client) Revoke(ctx context.Context, a appcontrol.Authority) error {
	_, err := c.appExchange(ctx, "app_revoke", a, native.AppRequest{})
	return err
}

// QuiesceApps fences and drains only this resource's app operations.
func (c *Client) QuiesceApps(ctx context.Context, a appcontrol.Authority) error {
	_, err := c.appExchange(ctx, "app_quiesce", a, native.AppRequest{})
	return err
}

// ResumeApps explicitly opens a fresh lease after the native recovery barrier.
func (c *Client) ResumeApps(ctx context.Context, a appcontrol.Authority) error {
	_, err := c.appExchange(ctx, "app_resume", a, native.AppRequest{})
	return err
}

// DisposeApps restores owned windows before their virtual display is destroyed.
func (c *Client) DisposeApps(ctx context.Context, a appcontrol.Authority) error {
	_, err := c.appExchange(ctx, "app_dispose", a, native.AppRequest{})
	return err
}

// LaunchApp launches only an installed bundle; process identities come from native readback.
func (c *Client) LaunchApp(ctx context.Context, a appcontrol.Authority, launch appcontrol.LaunchRequest) (appcontrol.Window, error) {
	out, err := c.appExchange(ctx, "app_launch", a, native.AppRequest{Launch: &launch})
	if err != nil {
		return appcontrol.Window{}, err
	}
	if out == nil || out.Window == nil {
		return appcontrol.Window{}, native.ErrProtocol
	}
	return *out.Window, nil
}

// ActApp executes one fenced, non-replayable native action.
func (c *Client) ActApp(ctx context.Context, r protocol.VscreenActionRequest) (appcontrol.Result, error) {
	t := r.Target
	a := appcontrol.Authority{Resource: t.Resource, Epoch: t.Epoch, TaskID: t.TaskID, TransactionID: t.TransactionID, LeaseEpoch: t.LeaseEpoch}
	out, err := c.appExchange(ctx, "app_action", a, native.AppRequest{Action: &r})
	if err != nil {
		return appcontrol.Result{}, err
	}
	if out == nil || out.Result == nil {
		return appcontrol.Result{}, native.ErrProtocol
	}
	return *out.Result, nil
}

// GrantHuman accepts a daemon-minted local-owner capability, separate from task leases.
func (c *Client) GrantHuman(ctx context.Context, a appcontrol.Authority, g native.HumanGrant, ttl time.Duration) error {
	ms, err := ttlMillis(ttl)
	if err != nil {
		return err
	}
	_, err = c.appExchange(ctx, "app_human_grant", a, native.AppRequest{Human: &g, LeaseTTLMS: ms})
	return err
}

// TransferApp consumes one explicit local-owner grant after native quiescence.
func (c *Client) TransferApp(ctx context.Context, a appcontrol.Authority, g native.HumanGrant) (appcontrol.Window, error) {
	out, err := c.appExchange(ctx, "app_human_transfer", a, native.AppRequest{Human: &g})
	if err != nil {
		return appcontrol.Window{}, err
	}
	if out == nil || out.Window == nil {
		return appcontrol.Window{}, native.ErrProtocol
	}
	return *out.Window, nil
}

// ProbeAppPermissions performs only nonprompt native preflight checks.
func (c *Client) ProbeAppPermissions(ctx context.Context) (appcontrol.Permissions, error) {
	out, err := c.appExchange(ctx, "app_probe", appcontrol.Authority{Epoch: protocol.VscreenEpoch{NativeEpoch: c.epoch}}, native.AppRequest{})
	if err != nil {
		return appcontrol.Permissions{}, err
	}
	if out == nil || out.Permissions == nil {
		return appcontrol.Permissions{}, native.ErrProtocol
	}
	return *out.Permissions, nil
}
func snapshotID() (string, error) {
	var b [16]byte
	_, err := rand.Read(b[:])
	return hex.EncodeToString(b[:]), err
}

// RevokeObserver cancels only the matching observation capability and its in-flight reads.
func (c *Client) RevokeObserver(ctx context.Context, a appcontrol.Authority) error {
	_, err := c.appExchange(ctx, "app_observer_revoke", a, native.AppRequest{})
	return err
}
