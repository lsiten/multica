//go:build linux

package applicationhost

import "os"

func readBootID() (string, error) {
	raw, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return string(raw), err
}
