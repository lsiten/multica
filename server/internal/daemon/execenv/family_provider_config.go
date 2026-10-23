package execenv

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

func copyProviderConfigFile(source, target string) error {
	data, err := os.ReadFile(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return writeFileAtomic(target, data, 0600)
}

func prepareFamilyReasonixConfig(root string, values map[string]string) (string, error) {
	sourceEnv := reasonixEnv(values)
	sourceConfig := sourceEnv.userConfigLoadPath()
	config := map[string]any{}
	if sourceConfig != "" {
		data, err := os.ReadFile(sourceConfig)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if err == nil {
			if err := toml.Unmarshal(data, &config); err != nil {
				return "", fmt.Errorf("parse Reasonix owner config: %w", err)
			}
		}
	}
	permissions, err := reasonixOwnerPermissions(sourceConfig)
	if err != nil {
		return "", err
	}
	permissions, err = withReasonixAskDenied(permissions)
	if err != nil {
		return "", err
	}
	config["permissions"] = permissions
	encoded, err := toml.Marshal(config)
	if err != nil {
		return "", err
	}
	target := filepath.Join(root, "reasonix-home")
	if err := os.MkdirAll(target, 0700); err != nil {
		return "", err
	}
	if err := writeFileAtomic(filepath.Join(target, reasonixUserConfigFile), encoded, 0600); err != nil {
		return "", err
	}
	for _, name := range []string{".env", "credentials", "settings.json"} {
		if err := copyProviderConfigFile(filepath.Join(sourceEnv.homeDir(), name), filepath.Join(target, name)); err != nil {
			return "", fmt.Errorf("copy Reasonix %s: %w", name, err)
		}
	}
	return target, nil
}

// OMP v18.8.3 exports its capability registry. Load the private MCP file
// through the native parser, then give these run-owned servers precedence over
// ambient sources without moving the owner's authentication or profile state.
func prepareFamilyOmpMcpExtension(root, workDir string, raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	if !json.Valid(raw) {
		return "", errors.New("invalid OMP MCP configuration")
	}
	dir := filepath.Join(root, "omp-mcp-extension")
	configDir := filepath.Join(dir, ".omp")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return "", err
	}
	if err := writeFileAtomic(filepath.Join(configDir, "mcp.json"), raw, 0600); err != nil {
		return "", err
	}
	cwd, err := filepath.Abs(workDir)
	if err != nil {
		return "", err
	}
	encodedCwd, err := json.Marshal(cwd)
	if err != nil {
		return "", err
	}
	source := fmt.Sprintf(ompFamilyMcpExtensionSource, encodedCwd)
	if err := writeFileAtomic(filepath.Join(dir, "index.ts"), []byte(source), 0600); err != nil {
		return "", err
	}
	return dir, nil
}

const ompFamilyMcpExtensionSource = `import path from "node:path";
import { fileURLToPath } from "node:url";
import { loadCapability, registerProvider } from "@oh-my-pi/pi-coding-agent/capability";
const extensionDir = path.dirname(fileURLToPath(import.meta.url));
const configPath = path.join(extensionDir, ".omp", "mcp.json");
const codeDir = %s;
export default function () {
  registerProvider("mcps", {
    id: "multica-managed-mcp", displayName: "Multica", description: "Run-private MCP configuration", priority: 1000,
    async load() {
      const result = await loadCapability("mcps", { providers: ["native"], cwd: extensionDir });
      const items = result.items.filter(server => path.resolve(server._source.path) === configPath).map(server => {
        const item = { ...server, _source: { provider: "multica-managed-mcp", providerName: "Multica", path: configPath, level: "project" } };
        if (item.command) {
          item.cwd = path.resolve(codeDir, item.cwd || ".");
          if (!path.isAbsolute(item.command) && (item.command.includes("/") || item.command.includes(String.fromCharCode(92)))) {
            item.command = path.resolve(item.cwd, item.command);
          }
        }
        return item;
      });
      return { items, warnings: result.warnings };
    }
  });
}
`
