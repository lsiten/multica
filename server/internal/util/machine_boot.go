package util

import (
	"fmt"
	"strings"
)

// CurrentBootID reads the operating system's stable boot-session identity.
func CurrentBootID() (string, error) {
	raw, err := readMachineBootID()
	if err != nil {
		return "", fmt.Errorf("read machine boot identity: %w", err)
	}
	id, err := ParseUUID(strings.Trim(strings.TrimSpace(raw), "{}"))
	if err != nil {
		return "", fmt.Errorf("invalid machine boot identity: %w", err)
	}
	return UUIDToString(id), nil
}
