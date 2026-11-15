package agentdesktop

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultReleaseBaseURL    = "https://github.com/lisiten/multica/releases/latest/download"
	agentDesktopLicenseAsset = "agent-desktop-LICENSE"
	agentDesktopLicenseSHA   = "cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30"
	maxArchiveBytes          = 64 << 20
	maxExtractedBytes        = 96 << 20
)

type releaseAsset struct {
	Name       string
	Archive    string
	ArchiveSHA string
	Helpers    []string
}

var releaseAssets = map[string]releaseAsset{
	"darwin/arm64": {
		Name:       "macOS arm64",
		Archive:    "agent-desktop-v1.3.1-aarch64-apple-darwin.tar.gz",
		ArchiveSHA: "9b7b7cfc74892eab46c2c85d6cb7dc4863bf8dab0a222cfcfb8ae4604c69c86c",
		Helpers:    []string{"agent-desktop-macos-helper"},
	},
	"darwin/amd64": {
		Name:       "macOS x64",
		Archive:    "agent-desktop-v1.3.1-x86_64-apple-darwin.tar.gz",
		ArchiveSHA: "469da10cfb68928c58caff88b92b30526e0ca895ac06bc86b7f533b1d9b309b3",
		Helpers:    []string{"agent-desktop-macos-helper"},
	},
	"linux/arm64": {
		Name:       "Linux arm64",
		Archive:    "agent-desktop-v1.3.1-aarch64-unknown-linux-gnu.tar.gz",
		ArchiveSHA: "438990a703fbeb19775596c341c3701435e80adbfa1b2f64b82ff17567d5573b",
		Helpers:    []string{"agent-desktop-linux-clipboard-owner"},
	},
	"linux/amd64": {
		Name:       "Linux x64",
		Archive:    "agent-desktop-v1.3.1-x86_64-unknown-linux-gnu.tar.gz",
		ArchiveSHA: "ca77b6b75034eeb12e537dd8c0a4f2a3424119253f38b7e6bd79ba0d2fef1264",
		Helpers:    []string{"agent-desktop-linux-clipboard-owner"},
	},
	"windows/arm64": {
		Name:       "Windows arm64",
		Archive:    "agent-desktop-v1.3.1-aarch64-pc-windows-msvc.tar.gz",
		ArchiveSHA: "805f1f61890f9983fceaf38cda94a37a049c379435d65607db5cedd22c8ef97b",
	},
	"windows/amd64": {
		Name:       "Windows x64",
		Archive:    "agent-desktop-v1.3.1-x86_64-pc-windows-msvc.tar.gz",
		ArchiveSHA: "6ee4b1e078f4e78792e79f16e32e4a78b9919464aa3c55629b7e27ed53a08c13",
	},
}

// InstallOptions identifies the managed destination and testable transport.
// Production callers pass an empty ReleaseBaseURL and nil HTTPClient.
type InstallOptions struct {
	Home           string
	ReleaseBaseURL string
	HTTPClient     *http.Client
}

// Install downloads, verifies, and atomically replaces the fixed release.
func Install(ctx context.Context, options InstallOptions, output io.Writer) (string, error) {
	asset, ok := releaseAssets[platformKey()]
	if !ok {
		return "", &UnavailableError{Reason: fmt.Sprintf("unsupported platform %s", platformKey())}
	}
	home := strings.TrimSpace(options.Home)
	if home == "" {
		resolved, err := os.UserHomeDir()
		if err != nil {
			return "", &UnavailableError{Reason: fmt.Sprintf("resolve home: %v", err)}
		}
		home = resolved
	}
	target := filepath.Join(home, ".multica", "tools", "agent-desktop", Version)
	executable := filepath.Join(target, binaryName())
	if err := Verify(ctx, executable); err == nil {
		if err := verifyInstallation(executable); err == nil {
			if output != nil {
				_, _ = fmt.Fprintf(output, "agent-desktop %s is already installed at %s\n", Tag, target)
			}
			return target, nil
		}
	}
	baseURL := strings.TrimRight(strings.TrimSpace(options.ReleaseBaseURL), "/")
	if baseURL == "" {
		baseURL = defaultReleaseBaseURL
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}

	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", fmt.Errorf("create agent-desktop tools directory: %w", err)
	}
	staging, err := os.MkdirTemp(parent, "."+Version+".install-*")
	if err != nil {
		return "", fmt.Errorf("create agent-desktop staging directory: %w", err)
	}
	defer os.RemoveAll(staging)

	archivePath := filepath.Join(staging, "release.tar.gz")
	if err := downloadFile(ctx, client, baseURL+"/"+asset.Archive, archivePath, maxArchiveBytes); err != nil {
		return "", fmt.Errorf("download %s: %w", asset.Name, err)
	}
	if err := verifyFileSHA256(archivePath, asset.ArchiveSHA); err != nil {
		return "", fmt.Errorf("verify %s: %w", asset.Name, err)
	}
	if err := extractRelease(archivePath, staging, asset); err != nil {
		return "", fmt.Errorf("extract %s: %w", asset.Name, err)
	}
	licensePath := filepath.Join(staging, "LICENSE")
	if err := downloadFile(ctx, client, baseURL+"/"+agentDesktopLicenseAsset, licensePath, 1<<20); err != nil {
		return "", fmt.Errorf("download agent-desktop license: %w", err)
	}
	if err := verifyFileSHA256(licensePath, agentDesktopLicenseSHA); err != nil {
		return "", fmt.Errorf("verify agent-desktop license: %w", err)
	}
	if err := os.WriteFile(filepath.Join(staging, "VERSION"), []byte(Tag+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("write agent-desktop version: %w", err)
	}
	if err := os.Remove(archivePath); err != nil {
		return "", fmt.Errorf("remove downloaded archive: %w", err)
	}
	if err := writeChecksumManifest(staging, asset); err != nil {
		return "", err
	}
	if err := replaceDirectory(target, staging); err != nil {
		return "", err
	}
	if output != nil {
		_, _ = fmt.Fprintf(output, "Installed agent-desktop %s at %s\n", Tag, target)
	}
	return target, nil
}

func platformKey() string {
	return runtimeGOOS() + "/" + runtimeGOARCH()
}
