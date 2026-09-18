package daemon

import (
	"context"
	"sync"

	"github.com/multica-ai/multica/server/internal/vscreen"
	"github.com/multica-ai/multica/server/internal/vscreen/hostclient"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type vscreenNativeDriver struct {
	mu       sync.Mutex
	client   *hostclient.Client
	displays map[protocol.ResourceKey]vscreen.Display
	input    VscreenInputHandler
}

func (d *vscreenNativeDriver) setClient(c *hostclient.Client) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.client = c
}
func (d *vscreenNativeDriver) setInput(h VscreenInputHandler) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.input = h
}
func (d *vscreenNativeDriver) Ensure(ctx context.Context, key vscreen.ResourceKey) (vscreen.Display, error) {
	d.mu.Lock()
	c := d.client
	d.mu.Unlock()
	if c == nil {
		return vscreen.Display{}, hostclient.ErrClosed
	}
	response, err := c.Call(ctx, native.Request{Operation: "ensure", Resource: key, Width: 1600, Height: 900})
	if err != nil {
		return vscreen.Display{}, err
	}
	display := vscreen.Display{Resource: key, Epoch: response.Epoch, DisplayID: response.Display.ID}
	d.mu.Lock()
	d.displays[key] = display
	d.mu.Unlock()
	return display, nil
}

// reconcileReadback advances geometry only within the existing native display incarnation.
func (d *vscreenNativeDriver) reconcileReadback(expected vscreen.Display, response native.Response) (vscreen.Display, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	current, ok := d.displays[expected.Resource]
	if !ok || current != expected || response.Display == nil || response.Display.ID != expected.DisplayID ||
		response.Epoch.NativeEpoch != expected.Epoch.NativeEpoch || response.Epoch.DisplayGeneration != expected.Epoch.DisplayGeneration ||
		response.Epoch.GeometryRevision < expected.Epoch.GeometryRevision {
		return vscreen.Display{}, &vscreen.Error{Reason: protocol.VscreenStaleSnapshot}
	}
	if err := response.Epoch.Validate(); err != nil {
		return vscreen.Display{}, err
	}
	current.Epoch = response.Epoch
	d.displays[expected.Resource] = current
	return current, nil
}
func (d *vscreenNativeDriver) Act(ctx context.Context, a vscreen.Action) (vscreen.ActionResult, error) {
	d.mu.Lock()
	h := d.input
	d.mu.Unlock()
	if h == nil {
		return vscreen.ActionResult{}, &vscreen.Error{Reason: protocol.VscreenNativeUnavailable}
	}
	return h.Act(ctx, a)
}
func (d *vscreenNativeDriver) Quiesce(ctx context.Context, key vscreen.ResourceKey) error {
	d.mu.Lock()
	h := d.input
	display, ok := d.displays[key]
	c := d.client
	d.mu.Unlock()
	if h != nil {
		if err := h.Quiesce(ctx, key); err != nil {
			return err
		}
	}
	if !ok {
		return nil
	}
	response, err := c.Call(ctx, native.Request{Operation: "quiesce", Resource: key, Epoch: display.Epoch})
	if err != nil {
		return err
	}
	_, err = d.reconcileReadback(display, response)
	return err
}
func (d *vscreenNativeDriver) Dispose(ctx context.Context, key vscreen.ResourceKey) error {
	d.mu.Lock()
	h := d.input
	display, ok := d.displays[key]
	c := d.client
	d.mu.Unlock()
	if h != nil {
		if err := h.Dispose(ctx, key); err != nil {
			return err
		}
	}
	if !ok {
		return nil
	}
	_, err := c.Call(ctx, native.Request{Operation: "dispose", Resource: key, Epoch: display.Epoch})
	if err == nil {
		d.mu.Lock()
		delete(d.displays, key)
		d.mu.Unlock()
	}
	return err
}
