package execenv

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFamilyProviderConfigHomesKeepCodeAndOtherRunsUnchanged(t *testing.T) {
	for _, provider := range []string{"omp", "reasonix", "cursor"} {
		t.Run(provider, func(t *testing.T) {
			home, code := t.TempDir(), t.TempDir()
			source := filepath.Join(home, ".reasonix")
			if err := os.MkdirAll(source, 0700); err != nil {
				t.Fatal(err)
			}
			original := []byte("default_model = 'retained'\n[permissions]\ndeny = ['shell']\n")
			if err := os.WriteFile(filepath.Join(source, "config.toml"), original, 0600); err != nil {
				t.Fatal(err)
			}
			var firstFile string
			var firstBytes []byte
			for i, token := range []string{"first-private-token", "second-private-token"} {
				root := t.TempDir()
				params := PrepareParams{Provider: provider, IsolateLocalContext: true, McpConfig: json.RawMessage(`{"mcpServers":{"fetch":{"command":"echo","env":{"TOKEN":"` + token + `"}}}}`), PrivateEnvironment: map[string]string{"HOME": home, "PI_PROFILE": "", "OMP_PROFILE": ""}, ReasonixEnv: map[string]string{"HOME": home, "REASONIX_HOME": source}, Task: TaskContextForEnv{CodeWorkspaceIssueID: "family"}}
				physical := &Environment{RootDir: root, WorkDir: code, MulticaConfigRoot: filepath.Join(root, "multica-config")}
				var prepared *Environment
				if i == 0 {
					var err error
					prepared, err = PreparePrivate(params, physical, testLogger())
					if err != nil {
						t.Fatal(err)
					}
				} else {
					prepared = ReusePrivate(ReuseParams{Provider: provider, IsolateLocalContext: true, McpConfig: params.McpConfig, PrivateEnvironment: params.PrivateEnvironment, ReasonixEnv: params.ReasonixEnv, Task: params.Task}, physical, testLogger())
					if prepared == nil {
						t.Fatal("reused family provider configuration failed")
					}
				}
				var configFile string
				switch provider {
				case "omp":
					configFile = filepath.Join(prepared.OmpMcpExtensionDir, ".omp", "mcp.json")
				case "reasonix":
					configFile = filepath.Join(prepared.ReasonixHome, "config.toml")
				case "cursor":
					configFile = filepath.Join(prepared.CursorMcpPluginDir, "mcp.json")
				}
				data, err := os.ReadFile(configFile)
				if err != nil {
					t.Fatal(err)
				}
				if provider == "reasonix" {
					if !strings.Contains(string(data), "ask") || !strings.Contains(string(data), "shell") || !strings.Contains(string(data), "retained") {
						t.Fatalf("owner policy lost: %s", data)
					}
				} else if !strings.Contains(string(data), token) {
					t.Fatal("run token missing")
				}
				if i == 0 {
					firstFile, firstBytes = configFile, data
				} else {
					if firstFile == configFile {
						t.Fatal("shared provider configuration")
					}
					got, err := os.ReadFile(firstFile)
					if err != nil || string(got) != string(firstBytes) {
						t.Fatal("second run rewrote first config")
					}
				}
			}
			entries, err := os.ReadDir(code)
			if err != nil || len(entries) != 0 {
				t.Fatalf("shared code received sidecars: %v %v", entries, err)
			}
			got, err := os.ReadFile(filepath.Join(source, "config.toml"))
			if err != nil || string(got) != string(original) {
				t.Fatal("owner config changed")
			}
		})
	}
}

func TestFamilyReasonixConfigCopiesCredentialSourcePrivately(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, ".env"), []byte("API_KEY=fake-owner-credential\n"), 0600); err != nil {
		t.Fatal(err)
	}
	target, err := prepareFamilyReasonixConfig(t.TempDir(), map[string]string{"REASONIX_HOME": source})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(target, ".env"))
	if err != nil || !strings.Contains(string(got), "fake-owner-credential") {
		t.Fatal("credential source lost")
	}
	info, err := os.Stat(filepath.Join(target, ".env"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("credentials are not private")
	}
	if err := os.WriteFile(filepath.Join(target, ".env"), []byte("task-only"), 0600); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(source, ".env"))
	if err != nil || !strings.Contains(string(original), "fake-owner-credential") {
		t.Fatal("task rewrote owner credentials")
	}
}

func TestFamilyReasonixPreparationKeepsSourceSelectorWithoutAmbientSecrets(t *testing.T) {
	source := []string{"HOME=/owner", "REASONIX_HOME=/custom-reasonix", "UNRELATED_SECRET=hidden"}
	reasonix := TaskPrivatePreparationEnvironment("reasonix", source, nil)
	if reasonix["REASONIX_HOME"] != "/custom-reasonix" || reasonix["UNRELATED_SECRET"] != "" {
		t.Fatalf("Reasonix selector environment lost or overbroad: %v", reasonix)
	}
}

func TestFamilyOmpExtensionUsesPublicRegistryWithoutMovingAuthentication(t *testing.T) {
	root, code := t.TempDir(), t.TempDir()
	raw := json.RawMessage(`{"mcpServers":{"private":{"command":"./server","cwd":"tools","env":{"TOKEN":"run-private"},"timeout":5000},"http":{"type":"http","url":"https://example.invalid/mcp","headers":{"Authorization":"private-header"},"auth":{"type":"apikey","credentialId":"owner"},"instructions":false},"path-command":{"command":"python","args":["server.py"]}}}`)
	dir, err := prepareFamilyOmpMcpExtension(root, code, raw)
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join(dir, "index.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "run-private") || strings.Contains(string(source), "agent.db") || strings.Contains(string(source), "PI_CONFIG_DIR") {
		t.Fatal("extension source moved credentials or embedded secret")
	}
	config := filepath.Join(dir, ".omp", "mcp.json")
	got, err := os.ReadFile(config)
	if err != nil || string(got) != string(raw) {
		t.Fatal("private MCP payload changed")
	}
	for _, name := range []string{filepath.Join(dir, "index.ts"), config} {
		info, err := os.Stat(name)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("private file permissions: %s %v", name, err)
		}
	}
	entries, err := os.ReadDir(code)
	if err != nil || len(entries) != 0 {
		t.Fatal("MCP wrote shared code")
	}
	runFamilyOmpExtensionProbe(t, root, dir, code, source)
}

// Execute only the generated adapter against the public registry contract.
// No installed provider CLI, provider account, network or model is used.
func runFamilyOmpExtensionProbe(t *testing.T, root, dir, code string, source []byte) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for the model-free extension contract probe")
	}
	pkg := filepath.Join(root, "node_modules", "@oh-my-pi", "pi-coding-agent")
	if err := os.MkdirAll(pkg, 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		filepath.Join(pkg, "package.json"):   []byte(`{"name":"@oh-my-pi/pi-coding-agent","type":"module","exports":{"./capability":"./capability.mjs"}}`),
		filepath.Join(pkg, "capability.mjs"): []byte(ompRegistryContractStub),
		filepath.Join(dir, "index.mjs"):      source,
		filepath.Join(root, "probe.mjs"):     []byte(ompGeneratedExtensionProbe),
	}
	for path, data := range files {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(t.Context(), node, filepath.Join(root, "probe.mjs"), code)
	cmd.Env = []string{"PATH=" + filepath.Dir(node), "HOME=" + t.TempDir()}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated extension contract: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}

const ompRegistryContractStub = `import fs from "node:fs";
import path from "node:path";
const providers = [];
export function registerProvider(capability, provider) {
  if (capability !== "mcps") throw new Error("unexpected capability");
  providers.push(provider);
  providers.sort((a, b) => b.priority - a.priority);
}
export async function loadCapability(capability, options = {}) {
  if (options.providers) {
    if (JSON.stringify(options.providers) !== '["native"]') throw new Error("recursive discovery");
    const file = path.join(options.cwd, ".omp", "mcp.json");
    const config = JSON.parse(fs.readFileSync(file, "utf8"));
    const items = Object.entries(config.mcpServers).map(([name, server]) => ({ ...server, name, transport: server.type, _source: { provider: "native", providerName: "OMP", path: file, level: "project" } }));
    items.push({ name: "unrelated", command: "ambient-must-not-be-promoted", _source: { provider: "native", providerName: "OMP", path: "/owner/mcp.json", level: "user" } });
    return { items, warnings: [] };
  }
  const seen = new Set(), items = [];
  for (const provider of providers) {
    for (const item of (await provider.load()).items) {
      if (!seen.has(item.name)) { seen.add(item.name); items.push(item); }
    }
  }
  return { items };
}
`

const ompGeneratedExtensionProbe = `import assert from "node:assert/strict";
import path from "node:path";
import { registerProvider, loadCapability } from "@oh-my-pi/pi-coding-agent/capability";
import extension from "./omp-mcp-extension/index.mjs";
registerProvider("mcps", { id: "native", priority: 100, load: async () => ({ items: [
  { name: "private", command: "owner-command", env: { TOKEN: "owner-token" } },
  { name: "unrelated", command: "owner-tool" }
] }) });
extension();
const result = await loadCapability("mcps");
const servers = Object.fromEntries(result.items.map(server => [server.name, server]));
const code = process.argv[2];
assert.equal(servers.private.env.TOKEN, "run-private");
assert.equal(servers.private.timeout, 5000);
assert.equal(servers.private.cwd, path.join(code, "tools"));
assert.equal(servers.private.command, path.join(code, "tools", "server"));
assert.equal(servers.http.transport, "http");
assert.equal(servers.http.headers.Authorization, "private-header");
assert.equal(servers.http.auth.credentialId, "owner");
assert.equal(servers.http.instructions, false);
assert.equal(servers["path-command"].command, "python");
assert.deepEqual(servers["path-command"].args, ["server.py"]);
assert.equal(servers["path-command"].cwd, code);
assert.equal(servers.unrelated.command, "owner-tool");
assert.equal(result.items.length, 4);
console.log("PASS generated extension: managed name wins; payload preserved; cwd normalized; ambient source excluded; unrelated source retained");
`
