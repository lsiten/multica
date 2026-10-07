package applicationhost

import (
	"fmt"
	"strings"

	"github.com/multica-ai/multica/server/internal/util"
)

// CurrentBootID reads the operating system's stable boot-session identity.
func CurrentBootID() (string, error) {
	raw, err := readBootID()
	if err != nil {
		return "", fmt.Errorf("read application machine boot identity: %w", err)
	}
	id, err := util.ParseUUID(strings.Trim(strings.TrimSpace(raw), "{}"))
	if err != nil {
		return "", fmt.Errorf("invalid application machine boot identity: %w", err)
	}
	return util.UUIDToString(id), nil
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
