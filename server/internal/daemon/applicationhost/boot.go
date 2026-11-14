package applicationhost

import "github.com/multica-ai/multica/server/internal/util"

// CurrentBootID reads the operating system's stable boot-session identity.
func CurrentBootID() (string, error) {
	return util.CurrentBootID()
}

// PreviousBoot proves that a private record belongs to a boot whose processes cannot still exist.
func PreviousBoot(record Record) (bool, error) {
	if record.BootID == "" {
		return false, nil
	}
	current, err := CurrentBootID()
	if err != nil {
		return false, err
	}
	return record.BootID != current, nil
}
