package daemon

import (
	"github.com/multica-ai/multica/server/internal/vscreen/native/globalinput"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type heldNativeInput struct {
	grant   protocol.MirrorControlGrant
	gesture string
	release func() error
}

func (h heldNativeInput) belongsTo(grant protocol.MirrorControlGrant, gesture string) bool {
	return h.grant.EqualIdentity(grant) && h.gesture == gesture
}

// ReleaseInput uses the original native coordinates and injector, even if the
// display or grant has disappeared. A later owner's input is never released.
func (b *mirrorControlBackend) ReleaseInput(grant protocol.MirrorControlGrant, gesture string) {
	b.inputMu.Lock()
	defer b.inputMu.Unlock()
	for key, held := range b.held {
		if !held.belongsTo(grant, gesture) {
			continue
		}
		if err := held.release(); err != nil {
			if b.d.logger != nil {
				b.d.logger.Warn("mirror held input release failed", "error", err)
			}
			continue
		}
		delete(b.held, key)
	}
}

func (b *mirrorControlBackend) dispatchPointer(injector globalinput.Injector, grant protocol.MirrorControlGrant, gesture string, event globalinput.PointerEvent) string {
	b.inputMu.Lock()
	defer b.inputMu.Unlock()
	key := "pointer:" + string(event.Button)
	held, exists := b.held[key]
	if exists && !held.belongsTo(grant, gesture) {
		return protocol.MirrorInputBusy
	}
	if !exists && (event.Kind == protocol.MirrorInputPointerUp || (event.Kind == protocol.MirrorInputPointerMove && event.Button != "")) {
		return protocol.MirrorInputStale
	}
	if err := injector.Pointer(event); err != nil {
		return injectorErrorReason(err)
	}
	switch event.Kind {
	case protocol.MirrorInputPointerDown, protocol.MirrorInputPointerMove:
		if event.Kind == protocol.MirrorInputPointerDown || exists {
			event.Kind = protocol.MirrorInputPointerUp
			b.held[key] = heldNativeInput{grant: grant, gesture: gesture, release: func() error { return injector.Pointer(event) }}
		}
	case protocol.MirrorInputPointerUp:
		delete(b.held, key)
	}
	return ""
}

func (b *mirrorControlBackend) dispatchKey(injector globalinput.Injector, grant protocol.MirrorControlGrant, gesture string, event globalinput.KeyEvent) string {
	b.inputMu.Lock()
	defer b.inputMu.Unlock()
	keyName := event.Key
	if keyName == "enter" {
		keyName = "return"
	}
	key := "key:" + keyName
	held, exists := b.held[key]
	if exists && !held.belongsTo(grant, gesture) {
		return protocol.MirrorInputBusy
	}
	if !event.Down && !exists {
		return protocol.MirrorInputStale
	}
	if err := injector.Key(event); err != nil {
		return injectorErrorReason(err)
	}
	if event.Down {
		event.Down = false
		event.Modifiers = nil
		b.held[key] = heldNativeInput{grant: grant, gesture: gesture, release: func() error { return injector.Key(event) }}
	} else {
		delete(b.held, key)
	}
	return ""
}
