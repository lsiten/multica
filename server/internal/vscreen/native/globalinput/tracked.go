package globalinput

import (
	"errors"
	"sync"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type trackedInjector struct {
	Injector
	mu       sync.Mutex
	pointers map[Button]PointerEvent
	keys     map[string]KeyEvent
}

func newTrackedInjector(injector Injector) Injector {
	return &trackedInjector{Injector: injector, pointers: make(map[Button]PointerEvent), keys: make(map[string]KeyEvent)}
}

func (i *trackedInjector) Pointer(event PointerEvent) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if err := i.Injector.Pointer(event); err != nil {
		return err
	}
	switch event.Kind {
	case protocol.MirrorInputPointerDown:
		i.pointers[event.Button] = event
	case protocol.MirrorInputPointerMove:
		if _, held := i.pointers[event.Button]; held {
			i.pointers[event.Button] = event
		}
	case protocol.MirrorInputPointerUp:
		delete(i.pointers, event.Button)
	}
	return nil
}

func (i *trackedInjector) Key(event KeyEvent) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if err := i.Injector.Key(event); err != nil {
		return err
	}
	key := event.Key
	if key == "enter" {
		key = "return"
	}
	if event.Down {
		i.keys[key] = event
	} else {
		delete(i.keys, key)
	}
	return nil
}

func (i *trackedInjector) Close() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	var result error
	for button, event := range i.pointers {
		event.Kind = protocol.MirrorInputPointerUp
		if err := i.Injector.Pointer(event); err != nil {
			result = errors.Join(result, err)
		} else {
			delete(i.pointers, button)
		}
	}
	for key, event := range i.keys {
		event.Down = false
		event.Modifiers = nil
		if err := i.Injector.Key(event); err != nil {
			result = errors.Join(result, err)
		} else {
			delete(i.keys, key)
		}
	}
	return errors.Join(result, i.Injector.Close())
}
