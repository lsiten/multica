package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon"
)

// The same reserved path serves direct and Desktop-owned smoke. No native work
// can precede the GUI gate; the producer validates the private config again.
func runVscreenPerformanceSmoke(out io.Writer, directory string) error {
	if os.Getenv("MULTICA_RUN_VSCREEN_GUI_SMOKE") != "1" {
		return errors.New("gui_not_authorized")
	}
	if !filepath.IsAbs(directory) {
		return errors.New("absolute_evidence_directory_required")
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		return errors.New("private_performance_directory_required")
	}
	configPath := filepath.Join(directory, "performance-config.json")
	if _, err = os.Lstat(configPath); os.IsNotExist(err) {
		var token [32]byte
		if _, err = rand.Read(token[:]); err != nil {
			return err
		}
		config := daemon.VscreenPerformanceSmokeConfig{SchemaVersion: 1, Nonce: hex.EncodeToString(token[:]), ParentPID: os.Getppid(), EvidenceDir: directory, Mode: "acceptance", DurationMS: 1800000, Cycles: 30, Requested: daemon.VscreenPerformanceRequest{Width: 1600, Height: 900, FPS: 30}}
		raw, err := json.Marshal(config)
		if err != nil {
			return err
		}
		file, err := os.OpenFile(configPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(raw)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			return errors.Join(writeErr, closeErr)
		}
	} else if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 2700*time.Second)
	defer cancel()
	return daemon.RunVscreenPerformanceSmoke(ctx, configPath, version+"/"+commit, out)
}
