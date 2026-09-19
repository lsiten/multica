package daemon

import (
	"sync"

	"github.com/multica-ai/multica/server/internal/vscreen/native/globalinput"
)

// globalInjectorHolder lazily constructs one platform global injector per
// daemon. The injector itself is stateless apart from OS permission state.
var (
	globalInjectorOnce sync.Once
	globalInjectorInst globalinput.Injector
)

func (d *Daemon) globalInjector() globalinput.Injector {
	globalInjectorOnce.Do(func() {
		globalInjectorInst = globalinput.New()
	})
	return globalInjectorInst
}
