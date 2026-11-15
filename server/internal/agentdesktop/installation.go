package agentdesktop

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func verifyInstallation(executable string) error {
	directory := filepath.Dir(executable)
	manifestPath := filepath.Join(directory, "SHA256SUMS")
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		return &UnavailableError{Path: directory, Reason: "SHA256SUMS is missing"}
	}
	entries := make(map[string]string)
	for lineNumber, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || len(fields[0]) != sha256.Size*2 {
			return &UnavailableError{Path: manifestPath, Reason: fmt.Sprintf("invalid checksum line %d", lineNumber+1)}
		}
		if filepath.Base(fields[1]) != fields[1] || fields[1] == "." || fields[1] == ".." {
			return &UnavailableError{Path: manifestPath, Reason: fmt.Sprintf("unsafe checksum path %q", fields[1])}
		}
		entries[fields[1]] = strings.ToLower(fields[0])
	}
	required := append([]string{filepath.Base(executable), "VERSION", "LICENSE"}, PlatformHelpers()...)
	for _, name := range required {
		expected, ok := entries[name]
		if !ok {
			return &UnavailableError{Path: directory, Reason: name + " is missing from SHA256SUMS"}
		}
		actual, hashErr := fileSHA256(filepath.Join(directory, name))
		if hashErr != nil {
			return &UnavailableError{Path: filepath.Join(directory, name), Reason: hashErr.Error()}
		}
		if actual != expected {
			return &UnavailableError{Path: filepath.Join(directory, name), Reason: "checksum mismatch"}
		}
	}
	version, err := os.ReadFile(filepath.Join(directory, "VERSION"))
	if err != nil || strings.TrimSpace(string(version)) != Tag {
		return &VersionError{Path: filepath.Join(directory, "VERSION"), Got: strings.TrimSpace(string(version))}
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
