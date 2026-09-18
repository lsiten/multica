// Package smokefixture owns the explicitly authorized, disposable native smoke app.
package smokefixture

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const ExecutableName = "MulticaVscreenFixture"
const guiEnvironment = "MULTICA_RUN_VSCREEN_GUI_SMOKE"

var ErrUnauthorized = errors.New("gui_not_authorized")
var ErrUnsupported = errors.New("fixture_unsupported")

type Foreground struct {
	PID      int     `json:"pid"`
	WindowID uint32  `json:"window_id"`
	CursorX  float64 `json:"cursor_x"`
	CursorY  float64 `json:"cursor_y"`
}
type WindowBounds struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}
type State struct {
	DisplayID    uint32       `json:"display_id"`
	Bounds       WindowBounds `json:"bounds"`
	HumanStage   uint64       `json:"human_stage"`
	Nonce        string       `json:"nonce"`
	PID          int          `json:"pid"`
	ProcessStart string       `json:"process_start"`
	WindowID     uint32       `json:"window_id"`
	Presses      uint64       `json:"presses"`
	Text         string       `json:"text"`
	Keys         uint64       `json:"keys"`
	Scrolls      uint64       `json:"scrolls"`
	Drags        uint64       `json:"drags"`
	Closed       bool         `json:"closed"`
}
type configuration struct {
	Nonce        string `json:"nonce"`
	BundleID     string `json:"bundle_id"`
	Directory    string `json:"directory"`
	OwnerPID     int    `json:"owner_pid"`
	OwnerStart   string `json:"owner_start"`
	BinarySHA256 string `json:"binary_sha256"`
}

// App identifies only the private, unique fixture created for this one invocation.
type App struct {
	launchAttempted bool
	BundleID        string `json:"bundle_id"`
	BinarySHA256    string `json:"binary_sha256"`
	Directory       string `json:"directory"`
	nonce           string
	bundle          string
}

func authorized() error {
	if os.Getenv(guiEnvironment) != "1" {
		return ErrUnauthorized
	}
	return nil
}
func IsFixtureExecutable() bool {
	p, err := os.Executable()
	return err == nil && filepath.Base(p) == ExecutableName
}

// Prepare copies the exact selected helper, never builds or launches an unrelated app.
func Prepare(executable, evidence string) (*App, error) {
	if err := authorized(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(executable) || !filepath.IsAbs(evidence) {
		return nil, errors.New("absolute_paths_required")
	}
	identity, err := processStart(os.Getpid())
	if err != nil {
		return nil, err
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(nonce[:])
	directory := filepath.Join(evidence, "fixture-"+token)
	if err = os.Mkdir(directory, 0700); err != nil {
		return nil, err
	}
	app := &App{BundleID: "ai.multica.smoke." + token, Directory: directory, nonce: token, bundle: filepath.Join(directory, "Fixture.app")}
	if err = prepareFiles(executable, app, identity); err != nil {
		os.RemoveAll(directory)
		return nil, err
	}
	if err = register(app.bundle); err != nil {
		os.RemoveAll(directory)
		return nil, err
	}
	return app, nil
}
func prepareFiles(executable string, app *App, ownerStart string) error {
	macOS := filepath.Join(app.bundle, "Contents", "MacOS")
	if err := os.MkdirAll(macOS, 0700); err != nil {
		return err
	}
	source, err := os.Open(executable)
	if err != nil {
		return err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("invalid_fixture_source")
	}
	target, err := os.OpenFile(filepath.Join(macOS, ExecutableName), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return err
	}
	digest := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(target, digest), source)
	closeErr := target.Close()
	if copyErr != nil || closeErr != nil {
		return errors.Join(copyErr, closeErr)
	}
	app.BinarySHA256 = hex.EncodeToString(digest.Sum(nil))
	// LaunchServices passes this opt-in only to the app created after the parent GUI gate.
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>%s</string><key>CFBundleExecutable</key><string>%s</string><key>CFBundleName</key><string>Multica Smoke Fixture</string><key>CFBundlePackageType</key><string>APPL</string><key>LSUIElement</key><true/><key>LSEnvironment</key><dict><key>MULTICA_RUN_VSCREEN_GUI_SMOKE</key><string>1</string></dict></dict></plist>`, app.BundleID, ExecutableName)
	if err = os.WriteFile(filepath.Join(app.bundle, "Contents", "Info.plist"), []byte(plist), 0600); err != nil {
		return err
	}
	cfg := configuration{Nonce: app.nonce, BundleID: app.BundleID, Directory: app.Directory, OwnerPID: os.Getpid(), OwnerStart: ownerStart, BinarySHA256: app.BinarySHA256}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(app.bundle, "Contents", "fixture.json"), raw, 0600)
}

// Run creates only this fixture's own window, after validating its opt-in and private config.
func Run() error {
	if err := authorized(); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if filepath.Base(executable) != ExecutableName {
		return errors.New("fixture_executable_required")
	}
	contents := filepath.Dir(filepath.Dir(executable))
	path := filepath.Join(contents, "fixture.json")
	raw, err := readPrivate(path, 4096)
	if err != nil {
		return err
	}
	var cfg configuration
	if json.Unmarshal(raw, &cfg) != nil || len(cfg.Nonce) != 32 || cfg.BundleID != "ai.multica.smoke."+cfg.Nonce || cfg.Directory != filepath.Dir(filepath.Dir(contents)) || cfg.OwnerPID <= 0 || cfg.OwnerStart == "" {
		return errors.New("invalid_fixture_config")
	}
	file, err := os.Open(executable)
	if err != nil {
		return err
	}
	digest := sha256.New()
	_, err = io.Copy(digest, file)
	file.Close()
	if err != nil {
		return err
	}
	if cfg.BinarySHA256 != hex.EncodeToString(digest.Sum(nil)) {
		return errors.New("fixture_binary_changed")
	}
	return run(raw)
}
func readPrivate(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > limit {
		return nil, errors.New("invalid_fixture_file")
	}
	return os.ReadFile(path)
}
func (a *App) Read() (State, error) {
	raw, err := readPrivate(filepath.Join(a.Directory, "readback.json"), 8192)
	if err != nil {
		return State{}, err
	}
	var state State
	if json.Unmarshal(raw, &state) != nil || state.Nonce != a.nonce || state.PID <= 0 || state.ProcessStart == "" {
		return State{}, errors.New("invalid_fixture_readback")
	}
	return state, nil
}

// BeforeLaunch marks the point after which asynchronous LaunchServices work may exist.
func (a *App) BeforeLaunch() { a.launchAttempted = true }

// Stop requests self-termination using the one-use fixture nonce; it never signals another PID.
func (a *App) Stop(ctx context.Context) error {
	if err := authorized(); err != nil {
		return err
	}
	if !a.launchAttempted {
		return os.RemoveAll(a.bundle)
	}
	if err := os.WriteFile(filepath.Join(a.Directory, "stop"), []byte(a.nonce), 0600); err != nil {
		return err
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := a.Read()
		if err == nil && state.Closed {
			start, e := processStart(state.PID)
			if e != nil || start != state.ProcessStart {
				return os.RemoveAll(a.bundle)
			}
		}
		select {
		case <-ctx.Done():
			return errors.New("fixture_cleanup_unconfirmed")
		case <-ticker.C:
		}
	}
}

// Snapshot is read-only and is never called by default tests without explicit opt-in.
func Snapshot() (Foreground, error) {
	if err := authorized(); err != nil {
		return Foreground{}, err
	}
	return snapshot()
}

// MarkHumanStage changes only the owned fixture through its private nonce file.
// It is scripted fixture readback, never a claim of manual human input.
func (a *App) MarkHumanStage() error {
	if err := authorized(); err != nil {
		return err
	}
	if !a.launchAttempted {
		return errors.New("fixture_not_launched")
	}
	return os.WriteFile(filepath.Join(a.Directory, "human-stage"), []byte(a.nonce), 0600)
}
