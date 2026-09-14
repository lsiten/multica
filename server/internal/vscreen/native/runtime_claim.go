package native

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"

	"github.com/multica-ai/multica/server/internal/vscreen/appclaim"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func acquireRuntimeClaim(key protocol.ResourceKey) (*appclaim.Lock, error) {
	account, err := user.LookupId(strconv.Itoa(os.Getuid()))
	if err != nil {
		return nil, err
	}
	// Resolve the OS account, not HOME: provider environments must not split ownership.
	directory := filepath.Join(account.HomeDir, ".multica", "native-claims")
	return appclaim.AcquireRuntime(directory, key)
}
