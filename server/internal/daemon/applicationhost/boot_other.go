//go:build !darwin && !linux && !windows

package applicationhost

import "errors"

func readBootID() (string, error) {
	return "", errors.New("machine boot identity is unavailable on this platform")
}
