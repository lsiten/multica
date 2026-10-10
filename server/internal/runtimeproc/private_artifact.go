package runtimeproc

import "os"

// ProtectPrivateArtifact applies the existing platform private-file policy to
// an already-open artifact. Callers separately validate their owned namespace.
func ProtectPrivateArtifact(file *os.File) error { return protectPrivateFile(file) }

// ValidatePrivateArtifact checks the existing platform owner/permission policy.
func ValidatePrivateArtifact(file *os.File) error { return validatePrivateFile(file) }

// ReplacePrivateArtifact uses the platform's durable atomic file replacement.
// Callers must bind both paths to their validated private artifact namespace.
func ReplacePrivateArtifact(from, to string) error { return replaceFile(from, to) }

// SyncPrivateArtifactDirectory persists an owned namespace after publication.
func SyncPrivateArtifactDirectory(path string) error { return syncDirectory(path) }
