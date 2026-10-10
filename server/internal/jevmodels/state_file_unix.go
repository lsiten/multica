//go:build !windows

package jevmodels

import "os"

func replaceStateFile(from, to string) error { return os.Rename(from, to) }
