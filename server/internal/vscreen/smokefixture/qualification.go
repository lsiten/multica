package smokefixture

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const QualificationExecutable = "MulticaInputQualification"
const QualificationHostCommand = "internal-vscreen-input-qualification-host"

// QualificationScope travels only over the isolated host's inherited descriptor.
// It is not a production input certificate or a native RPC field.
type QualificationScope struct {
	InteractiveScratch                                   bool
	ExpiresAt                                            int64
	Resource                                             protocol.ResourceKey
	Directory, Nonce, BundleID, BinarySHA256, OwnerStart string
	OwnerPID                                             int
}
type QualificationState struct {
	ManualStarted      bool   `json:"manual_started"`
	ManualConfirmed    bool   `json:"manual_confirmed"`
	KeyCount           uint64 `json:"key_count"`
	FirstKeyNS         int64  `json:"first_key_ns"`
	LastKeyNS          int64  `json:"last_key_ns"`
	LastProcessedToken uint64 `json:"last_processed_token,omitempty"`
	LastEventType      uint32 `json:"last_event_type"`
	State
	Toggle                             bool `json:"toggle"`
	SelectionLocation, SelectionLength uint64
	ScrollOffset, ObjectX, ObjectY     float64
	AXFocused                          bool   `json:"ax_focused"`
	InputSource                        string `json:"input_source"`
}

func IsQualificationExecutable() bool {
	p, e := os.Executable()
	return e == nil && filepath.Base(p) == QualificationExecutable
}
func (s QualificationScope) Executable() string {
	return filepath.Join(s.Directory, "Fixture.app/Contents/MacOS", QualificationExecutable)
}

func PrepareQualification(executable, evidence string, resource protocol.ResourceKey) (*App, QualificationScope, error) {
	return prepareQualification(executable, evidence, resource, false)
}
func PrepareQualificationScratch(executable, evidence string, resource protocol.ResourceKey) (*App, QualificationScope, error) {
	return prepareQualification(executable, evidence, resource, true)
}
func prepareQualification(executable, evidence string, resource protocol.ResourceKey, scratch bool) (*App, QualificationScope, error) {
	var empty QualificationScope
	if err := authorized(); err != nil {
		return nil, empty, err
	}
	if !filepath.IsAbs(executable) || !filepath.IsAbs(evidence) || resource.Validate() != nil {
		return nil, empty, errors.New("invalid_qualification_scope")
	}
	evidence, err := filepath.EvalSymlinks(evidence)
	if err != nil {
		return nil, empty, err
	}
	owner, err := processStart(os.Getpid())
	if err != nil {
		return nil, empty, err
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return nil, empty, err
	}
	token := hex.EncodeToString(nonce[:])
	directory := filepath.Join(evidence, "qualification-"+token)
	if err = os.Mkdir(directory, 0700); err != nil {
		return nil, empty, err
	}
	prepared := false
	defer func() {
		if !prepared {
			_ = os.RemoveAll(directory)
		}
	}()
	app := &App{BundleID: "ai.multica.smoke." + token, Directory: directory, nonce: token, bundle: filepath.Join(directory, "Fixture.app")}
	if err = prepareFiles(executable, app, owner); err != nil {
		return nil, empty, err
	}
	old := filepath.Join(app.bundle, "Contents/MacOS", ExecutableName)
	target := filepath.Join(app.bundle, "Contents/MacOS", QualificationExecutable)
	if err = os.Rename(old, target); err != nil {
		return nil, empty, err
	}
	plist := filepath.Join(app.bundle, "Contents/Info.plist")
	raw, err := os.ReadFile(plist)
	if err != nil {
		return nil, empty, err
	}
	if err = os.WriteFile(plist, []byte(strings.ReplaceAll(string(raw), ExecutableName, QualificationExecutable)), 0600); err != nil {
		return nil, empty, err
	}
	scope := QualificationScope{InteractiveScratch: scratch, ExpiresAt: time.Now().Add(90 * time.Second).UnixMilli(), Resource: resource, Directory: directory, Nonce: token, BundleID: app.BundleID, BinarySHA256: app.BinarySHA256, OwnerPID: os.Getpid(), OwnerStart: owner}
	raw, _ = json.Marshal(scope)
	if err = os.WriteFile(filepath.Join(app.bundle, "Contents/qualification.json"), raw, 0600); err != nil {
		return nil, empty, err
	}
	if err = register(app.bundle); err != nil {
		return nil, empty, err
	}
	prepared = true
	return app, scope, nil
}

// Validate binds the invocation to owned files and the live parent, never arbitrary app paths.
func (s QualificationScope) Validate(parent int, helper string) error {
	return s.validate(parent, helper, processStart)
}
func (s QualificationScope) validate(parent int, helper string, start func(int) (string, error)) error {
	if s.ExpiresAt <= time.Now().UnixMilli() || s.ExpiresAt > time.Now().Add(90*time.Second).UnixMilli() || authorized() != nil || s.Resource.Validate() != nil || s.Resource.UID != uint32(os.Getuid()) || s.OwnerPID != parent || len(s.Nonce) != 32 || s.BundleID != "ai.multica.smoke."+s.Nonce || !filepath.IsAbs(s.Directory) || filepath.Base(s.Directory) != "qualification-"+s.Nonce {
		return errors.New("invalid_qualification_scope")
	}
	if _, err := hex.DecodeString(s.Nonce); err != nil {
		return errors.New("invalid_qualification_scope")
	}
	owner, err := start(parent)
	if err != nil || owner != s.OwnerStart {
		return errors.New("stale_qualification_owner")
	}
	dir, err := os.Lstat(s.Directory)
	if err != nil || !dir.IsDir() || !qualificationOwnedDirectory(s.Directory) {
		return errors.New("unsafe_qualification_directory")
	}
	canonical, err := filepath.EvalSymlinks(s.Directory)
	if err != nil || canonical != s.Directory {
		return errors.New("unsafe_qualification_directory")
	}
	for _, path := range []string{helper, s.Executable()} {
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil || canonical != path {
			return errors.New("qualification_binary_changed")
		}
		raw, err := qualificationReadFile(path, 256<<20, path == s.Executable())
		if err != nil {
			return errors.New("qualification_binary_changed")
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != s.BinarySHA256 {
			return errors.New("qualification_binary_changed")
		}
	}
	raw, err := qualificationReadFile(filepath.Join(s.Directory, "Fixture.app/Contents/qualification.json"), 4096, true)
	if err != nil {
		return err
	}
	var disk QualificationScope
	if json.Unmarshal(raw, &disk) != nil || disk != s {
		return errors.New("qualification_scope_changed")
	}
	return nil
}
func ReadQualification(app *App) (QualificationState, error) {
	var state QualificationState
	raw, err := qualificationReadFile(filepath.Join(app.Directory, "readback.json"), 16384, true)
	if err != nil {
		return state, err
	}
	if json.Unmarshal(raw, &state) != nil || state.Nonce != app.nonce {
		return state, errors.New("invalid_fixture_readback")
	}
	return state, nil
}
func RunQualification() error {
	if authorized() != nil {
		return ErrUnauthorized
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	raw, err := qualificationReadFile(filepath.Join(filepath.Dir(filepath.Dir(executable)), "qualification.json"), 4096, true)
	if err != nil {
		return err
	}
	var scope QualificationScope
	if json.Unmarshal(raw, &scope) != nil || scope.Executable() != executable {
		return errors.New("invalid_qualification_scope")
	}
	// The fixture is an exact copy of the helper and is validated against its own bytes.
	if err = scope.Validate(scope.OwnerPID, executable); err != nil {
		return err
	}
	raw, _ = json.Marshal(scope)
	return runQualification(raw)
}
func (s QualificationScope) Read() (QualificationState, error) {
	return ReadQualification(&App{Directory: s.Directory, nonce: s.Nonce})
}

// ConsumeQualificationHost prevents the same private invocation from authorizing a second host.
func (s QualificationScope) ConsumeQualificationHost() error {
	file, err := os.OpenFile(filepath.Join(s.Directory, "host-consumed"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("qualification_already_consumed")
	}
	return file.Close()
}

// AdvanceQualificationPrompt changes only the displayed instruction, never input text.
func (s QualificationScope) AdvanceQualificationPrompt(stage int) error {
	if !s.InteractiveScratch || stage < 1 || stage > 6 {
		return errors.New("invalid_manual_stage")
	}
	raw, _ := json.Marshal(struct {
		Nonce string
		Stage int
	}{s.Nonce, stage})
	tmp := filepath.Join(s.Directory, "manual-progress.tmp")
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(raw)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(tmp)
		return errors.Join(writeErr, closeErr)
	}
	return os.Rename(tmp, filepath.Join(s.Directory, "manual-progress"))
}

// QualificationEffect checks rendered model state, never delivery counters.
func QualificationEffect(kind string, before, after QualificationState) bool {
	switch kind {
	case "click":
		return !before.Toggle && after.Toggle
	case "type", "unicode":
		return after.Text == "PID 中文 e\u0301Z" && after.SelectionLocation == uint64(len(utf16.Encode([]rune(after.Text)))) && after.SelectionLength == 0
	case "key":
		return before.Text == "PID 中文 e\u0301Z" && after.Text == "PID 中文 e\u0301" && after.SelectionLocation+1 == before.SelectionLocation && after.SelectionLength == 0
	case "scroll":
		return math.Abs(after.ScrollOffset-before.ScrollOffset) > 1
	case "drag":
		return math.Abs(after.ObjectX-before.ObjectX-100) < 3 && math.Abs(after.ObjectY-before.ObjectY) < 3
	}
	return false
}
