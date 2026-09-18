package hostclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
)

type pendingSnapshot struct {
	authority appcontrol.Authority
	window    string
	data      []byte
	total     uint32
	display   uint32
	err       error
	done      chan struct{}
	complete  bool
}

func (p *pendingSnapshot) finish(err error) {
	if p.complete {
		return
	}
	p.complete = true
	p.err = err
	close(p.done)
}
func (c *Client) acceptSnapshot(s native.MediaSample) {
	c.mediaMu.Lock()
	defer c.mediaMu.Unlock()
	p := c.snapshots[s.StreamID]
	if p == nil {
		return
	} // Cancelled or unsolicited IDs cannot allocate storage.
	if p.complete {
		p.err = native.ErrProtocol
		return
	}
	if s.Epoch != p.authority.Epoch || s.SnapshotTotal == 0 || s.SnapshotTotal > native.MaxMediaPayloadBytes || s.SnapshotOffset != uint32(len(p.data)) || p.total != 0 && p.total != s.SnapshotTotal || p.display != 0 && p.display != s.DisplayID {
		p.finish(native.ErrProtocol)
		return
	}
	if p.total == 0 {
		p.total = s.SnapshotTotal
		p.display = s.DisplayID
		p.data = make([]byte, 0, p.total)
	}
	p.data = append(p.data, s.PNG...)
	if uint32(len(p.data)) > p.total {
		p.finish(native.ErrProtocol)
		return
	}
	if uint32(len(p.data)) == p.total {
		p.finish(nil)
	}
}

// ObserveApp returns bounded AX metadata and, when requested, the fully verified PNG.
// A cancelled/failed image request never succeeds with metadata alone.
func (c *Client) ObserveApp(ctx context.Context, a appcontrol.Authority, window string, png bool) (appcontrol.Observation, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	request := native.AppRequest{WindowHandle: window, IncludePNG: png}
	var pending *pendingSnapshot
	if png {
		id, err := snapshotID()
		if err != nil {
			return appcontrol.Observation{}, err
		}
		request.SnapshotID = id
		pending = &pendingSnapshot{authority: a, window: window, done: make(chan struct{})}
		c.mediaMu.Lock()
		if c.media == nil || c.mediaErr != nil || len(c.snapshots) >= 4 || len(c.snapshotUsed) >= 4096 || c.snapshotUsed[id] {
			c.mediaMu.Unlock()
			return appcontrol.Observation{}, ErrClosed
		}
		c.snapshots[id] = pending
		c.snapshotUsed[id] = true
		c.mediaMu.Unlock()
		defer func() { c.mediaMu.Lock(); delete(c.snapshots, id); c.mediaMu.Unlock() }()
	}
	out, err := c.appExchange(ctx, "app_observe", a, request)
	if err != nil {
		return appcontrol.Observation{}, err
	}
	if out == nil || out.Observation == nil {
		return appcontrol.Observation{}, native.ErrProtocol
	}
	o := *out.Observation
	if o.Display.Resource != a.Resource || o.Display.Epoch != a.Epoch || !o.Display.Virtual || o.Display.ID == 0 || o.Window.Handle != window || len(o.PNG) != 0 {
		return appcontrol.Observation{}, native.ErrProtocol
	}
	if !png {
		if out.Snapshot != nil {
			return appcontrol.Observation{}, native.ErrProtocol
		}
		return o, nil
	}
	d := out.Snapshot
	if d == nil || d.ID != request.SnapshotID || d.Resource != a.Resource || d.Epoch != a.Epoch || d.WindowHandle != window || d.SnapshotRevision != o.Window.SnapshotRevision || d.DisplayID != o.Display.ID || d.Size == 0 || d.Size > native.MaxMediaPayloadBytes {
		return appcontrol.Observation{}, native.ErrProtocol
	}
	select {
	case <-ctx.Done():
		return appcontrol.Observation{}, ctx.Err()
	case <-pending.done:
	}
	c.mediaMu.Lock()
	defer c.mediaMu.Unlock()
	if pending.err != nil {
		return appcontrol.Observation{}, pending.err
	}
	sum := sha256.Sum256(pending.data)
	if pending.total != d.Size || pending.display != d.DisplayID || hex.EncodeToString(sum[:]) != d.SHA256 || !bytes.HasPrefix(pending.data, []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
		return appcontrol.Observation{}, native.ErrProtocol
	}
	o.PNG = pending.data
	return o, nil
}
