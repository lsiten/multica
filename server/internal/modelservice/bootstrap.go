package modelservice

import (
	"errors"
	"os"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/internal/util"
)

func newService(b runtimeproc.Bootstrap, m Manager) (*Service, error) {
	inv, err := readInventory(b.Root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if inv.DescendantsPossible {
		return nil, errors.New("model descendants from prior owner are unconfirmed")
	}
	if inv.Jobs == nil {
		inv.Jobs = map[string]InstallJob{}
	}
	for key, job := range inv.Jobs {
		if activeJob(job.State) {
			job.State = "interrupted"
			job.Error = "prior install was interrupted; explicit retry required"
			job.UpdatedAt = time.Now()
			inv.Jobs[key] = job
		}
	}
	inv.BootID, err = util.CurrentBootID()
	if err != nil {
		return nil, err
	}
	inv.InstanceID = b.Identity.InstanceID
	if err = saveInventory(b.Root, inv); err != nil {
		return nil, err
	}
	return &Service{bootstrap: b, manager: m, inventory: inv, leases: map[string]*leaseState{}, joined: make(chan struct{}), leaseTTL: leaseLifetime, acquisitions: map[string]*acquisition{}}, nil
}
