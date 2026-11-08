package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/native/globalinput"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (p *physicalVscreenTarget) drag(ctx context.Context, action *protocol.VscreenDragAction) (err error) {
	x, y := mapFrameToDisplay(action.From.X, action.From.Y, p.descriptor)
	event := globalinput.PointerEvent{Kind: protocol.MirrorInputPointerDown, Button: globalinput.ButtonLeft, X: x, Y: y}
	if err := p.injector.Pointer(event); err != nil {
		return err
	}
	// The caller retains arbitration until this release finishes, including
	// cancellation and failed movement after the successful down.
	defer func() {
		event.Kind = protocol.MirrorInputPointerUp
		err = errors.Join(err, p.injector.Pointer(event))
	}()
	timer := time.NewTimer(time.Duration(action.DurationMS) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	x, y = mapFrameToDisplay(action.To.X, action.To.Y, p.descriptor)
	move := globalinput.PointerEvent{Kind: protocol.MirrorInputPointerMove, Button: globalinput.ButtonLeft, X: x, Y: y}
	if err := p.injector.Pointer(move); err != nil {
		return err
	}
	event = move
	return nil
}
