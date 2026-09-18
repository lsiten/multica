package daemon

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

const VscreenTakeoverCoordinatorCommand = "internal-vscreen-takeover-coordinator"

type smokeCoordinatorConfig struct {
	VscreenTakeoverSmokeConfig
	Nonce    string
	OwnerPID int
}

func readSmokePrivateJSON(path string, target any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 65536 {
		return errors.New("invalid_private_smoke_config")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return strictVscreenJSON(raw, target)
}
func writeSmokePrivateJSON(path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0600)
}

// LaunchVscreenTakeoverSmoke confines production preparation to a dedicated child
// home and the selected binary; no installed provider or user account is used.
func LaunchVscreenTakeoverSmoke(ctx context.Context, c VscreenTakeoverSmokeConfig) (VscreenTakeoverSmokeEvidence, error) {
	var evidence VscreenTakeoverSmokeEvidence
	if os.Getenv("MULTICA_RUN_VSCREEN_GUI_SMOKE") != "1" {
		return evidence, errors.New("gui_not_authorized")
	}
	if c.Fixture != nil || !filepath.IsAbs(c.NativeExecutable) || !filepath.IsAbs(c.EvidenceDir) || c.NativeBuild == "" {
		return evidence, errors.New("invalid_smoke_paths")
	}
	private, err := os.MkdirTemp(c.EvidenceDir, "takeover-private-")
	if err != nil {
		return evidence, err
	}
	c.PrivateRoot = private
	for _, name := range []string{"home", "tmp"} {
		if err = os.Mkdir(filepath.Join(private, name), 0700); err != nil {
			return evidence, err
		}
	}
	nonce, err := randomBrokerToken()
	if err != nil {
		return evidence, err
	}
	path := filepath.Join(private, "coordinator.json")
	if err = writeSmokePrivateJSON(path, smokeCoordinatorConfig{VscreenTakeoverSmokeConfig: c, Nonce: nonce, OwnerPID: os.Getpid()}); err != nil {
		return evidence, err
	}
	command := exec.CommandContext(ctx, c.NativeExecutable, VscreenTakeoverCoordinatorCommand, path)
	command.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "HOME=" + filepath.Join(private, "home"), "TMPDIR=" + filepath.Join(private, "tmp"), "MULTICA_RUN_VSCREEN_GUI_SMOKE=1", smokePrivateNonceEnv + "=" + nonce}
	var output, stderr bytes.Buffer
	command.Stdout = &output
	command.Stderr = &stderr
	runErr := command.Run()
	if json.Unmarshal(output.Bytes(), &evidence) != nil {
		return evidence, errors.Join(runErr, errors.New("takeover_cleanup_unconfirmed"))
	}
	if runErr != nil || evidence.Error != "" {
		return evidence, errors.New("takeover_smoke_failed")
	}
	if err = os.RemoveAll(private); err != nil {
		return evidence, err
	}
	return evidence, nil
}

// RunVscreenTakeoverSmokeChild validates the private launcher before any native action.
func RunVscreenTakeoverSmokeChild(ctx context.Context, path, build string, out io.Writer) error {
	var config smokeCoordinatorConfig
	var evidence VscreenTakeoverSmokeEvidence
	err := func() error {
		if os.Getenv("MULTICA_RUN_VSCREEN_GUI_SMOKE") != "1" {
			return errors.New("gui_not_authorized")
		}
		if err := readSmokePrivateJSON(path, &config); err != nil {
			return err
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		if executable != config.NativeExecutable || build != config.NativeBuild || config.OwnerPID != os.Getppid() || len(config.Nonce) < 32 || subtle.ConstantTimeCompare([]byte(config.Nonce), []byte(os.Getenv(smokePrivateNonceEnv))) != 1 {
			return errors.New("invalid_takeover_launcher")
		}
		evidence, err = RunVscreenTakeoverSmoke(ctx, config.VscreenTakeoverSmokeConfig)
		return err
	}()
	if err != nil {
		evidence.Error = err.Error()
	}
	return errors.Join(err, json.NewEncoder(out).Encode(evidence))
}
