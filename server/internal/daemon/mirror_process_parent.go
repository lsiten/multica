package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (d *Daemon) bindMirrorProcess(ctx context.Context, generation mirrorConnectionGeneration, enqueue func([]byte) (*wsOutbound, error)) error {
	if !d.mirrorProcessMode() {
		return nil
	}
	client, err := d.ensureMirrorProcess(ctx)
	if err != nil {
		return err
	}
	resources, err := d.mirrorRoster()
	if err != nil {
		return err
	}
	d.mu.Lock()
	serverGeneration := d.vscreenServerGeneration
	d.mu.Unlock()
	return client.bind(ctx, mirrorConnectionBinding{Generation: uint64(generation), ServerGeneration: serverGeneration, Resources: resources}, enqueue)
}

func (d *Daemon) currentMirrorProcess() (*mirrorProcessClient, error) {
	d.mirrorProcessMu.Lock()
	defer d.mirrorProcessMu.Unlock()
	if d.mirrorProcess == nil || d.mirrorProcessClosed {
		return nil, errors.New("mirror service unavailable")
	}
	return d.mirrorProcess, nil
}

func (d *Daemon) mirrorCall(ctx context.Context, input mirrorProcessRequest, out any) error {
	client, err := d.currentMirrorProcess()
	if err != nil {
		return err
	}
	return client.call(ctx, input, out)
}

func (d *Daemon) forwardMirrorControl(ctx context.Context, kind string, msg mirrorOfferMessage) bool {
	if !d.mirrorProcessMode() {
		return false
	}
	client, err := d.currentMirrorProcess()
	if err == nil {
		client.mu.Lock()
		current := client.generation
		client.mu.Unlock()
		if current != uint64(msg.controlGeneration) {
			err = errors.New("stale control connection")
		} else {
			err = client.call(ctx, mirrorProcessRequest{Operation: "control", Payload: marshalRaw(protocol.Message{Type: kind, Payload: msg.raw})}, nil)
		}
	}
	if err != nil {
		if d.logger != nil {
			d.logger.Warn("mirror process control unavailable", "event", kind, "error", err)
		}
		var uncertain *mirrorUncertainOperation
		if msg.enqueue != nil && ctx.Err() == nil && !errors.As(err, &uncertain) && kind == protocol.EventMirrorOffer {
			var offer protocol.MirrorOfferPayload
			if json.Unmarshal(msg.raw, &offer) == nil && offer.Validate() == nil {
				_ = d.sendMirrorAnswerFailure(msg.enqueue, offer, protocol.MirrorAnswerFailureCaptureUnavailable)
			}
		}
	}
	return true
}

func (d *Daemon) closeMirrorProcess() {
	d.mirrorProcessMu.Lock()
	d.mirrorProcessClosed = true
	client := d.mirrorProcess
	d.mirrorProcessMu.Unlock()
	if client != nil {
		if err := client.close(); err != nil && d.logger != nil {
			d.logger.Warn("mirror process shutdown unconfirmed", "error", err)
		}
	}
}

func (d *Daemon) refreshMirrorRoster() {
	client, err := d.currentMirrorProcess()
	if err != nil {
		return
	}
	client.mu.Lock()
	generation, enqueue := client.generation, client.enqueue
	client.mu.Unlock()
	if generation == 0 || enqueue == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	d.mu.Lock()
	removed := d.mirrorPendingRemoval
	d.mirrorPendingRemoval = map[string]bool{}
	d.mu.Unlock()
	for runtimeID := range removed {
		if err = client.call(ctx, mirrorProcessRequest{Operation: "remove", RuntimeID: runtimeID}, nil); err != nil {
			client.unbind(generation)
			if d.logger != nil {
				d.logger.Warn("mirror removed runtime cleanup unconfirmed", "error", err)
			}
			return
		}
	}
	if err = d.bindMirrorProcess(ctx, mirrorConnectionGeneration(generation), enqueue); err != nil && d.logger != nil {
		d.logger.Warn("mirror roster refresh failed", "error", err)
	}
}
