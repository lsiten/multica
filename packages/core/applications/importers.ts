export type ApplicationImportFormat = "compose" | "procfile" | "package";

export interface ApplicationImportResult {
  readonly command: string[];
  readonly environment: Record<string, string>;
  readonly port: number;
  readonly entryPath: string;
  readonly source: string;
}

function splitCommand(value: string): string[] {
  const result: string[] = [];
  const pattern = /"([^"\\]*(?:\\.[^"\\]*)*)"|'([^']*)'|([^\s]+)/g;
  for (const match of value.trim().matchAll(pattern)) {
    result.push(match[1] ?? match[2] ?? match[3] ?? "");
  }
  return result;
}

function inferPort(command: string[], environment: Record<string, string>, fallback = 0): number {
  const candidates = [environment.PORT, command.find((part) => /^\d{2,5}$/.test(part)), command.join(" ").match(/(?:--port|-p|PORT=)\s*[=:]?\s*(\d{2,5})/)?.[1]];
  const port = Number(candidates.find((candidate) => candidate));
  return Number.isInteger(port) && port > 0 && port <= 65535 ? port : fallback;
}

function parseEnvironment(value: unknown): Record<string, string> {
  if (Array.isArray(value)) {
    return Object.fromEntries(value.flatMap((entry) => {
      if (typeof entry !== "string") return [];
      const index = entry.indexOf("=");
      return index > 0 ? [[entry.slice(0, index), entry.slice(index + 1)]] : [];
    }));
  }
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).flatMap(([key, item]) => typeof item === "string" || typeof item === "number" || typeof item === "boolean" ? [[key, String(item)]] : []));
  }
  return {};
}

function packageImport(text: string): ApplicationImportResult {
  const parsed = JSON.parse(text) as { scripts?: Record<string, unknown> };
  const scripts = parsed.scripts && typeof parsed.scripts === "object" ? parsed.scripts : {};
  const name = ["start", "dev", "serve"].find((candidate) => typeof scripts[candidate] === "string") ?? Object.keys(scripts).find((candidate) => typeof scripts[candidate] === "string");
  if (!name) throw new Error("No runnable package script found");
  const command = splitCommand(String(scripts[name]));
  return { command, environment: {}, port: inferPort(command, {}), entryPath: "/", source: `package.json:${name}` };
}

function procfileImport(text: string): ApplicationImportResult {
  const rows = text.split(/\r?\n/).map((line) => line.trim()).filter((line) => line && !line.startsWith("#"));
  const row = rows.find((line) => /^web\s*:/i.test(line)) ?? rows[0];
  const command = splitCommand(row?.replace(/^[^:]+:\s*/, "") ?? "");
  if (!command.length) throw new Error("No Procfile process found");
  return { command, environment: {}, port: inferPort(command, {}, 0), entryPath: "/", source: "Procfile" };
}

function yamlScalar(value: string): string {
  const trimmed = value.trim();
  if ((trimmed.startsWith("\"") && trimmed.endsWith("\"")) || (trimmed.startsWith("'") && trimmed.endsWith("'"))) return trimmed.slice(1, -1);
  return trimmed;
}

function composeImport(text: string): ApplicationImportResult {
  try {
    const parsed = JSON.parse(text) as { services?: Record<string, { command?: unknown; environment?: unknown; ports?: unknown }> };
    const service = Object.entries(parsed.services ?? {})[0]?.[1];
    if (service) {
      const command = Array.isArray(service.command) ? service.command.filter((part): part is string => typeof part === "string") : typeof service.command === "string" ? splitCommand(service.command) : [];
      const environment = parseEnvironment(service.environment);
      const portValue = Array.isArray(service.ports) ? String(service.ports[0] ?? "") : "";
      const port = Number(portValue.split(":").at(-1));
      return { command, environment, port: inferPort(command, environment, Number.isInteger(port) ? port : 0), entryPath: "/", source: "compose service" };
    }
  } catch {
    // Compose YAML is handled by the conservative line parser below.
  }
  let inService = false;
  let inEnvironment = false;
  let command: string[] = [];
  const environment: Record<string, string> = {};
  let port = 0;
  for (const rawLine of text.split(/\r?\n/)) {
    const line = rawLine.replace(/\t/g, "  ");
    if (/^\s{2}[\w.-]+:\s*$/.test(line)) { inService = true; inEnvironment = false; continue; }
    if (!inService) continue;
    if (/^\s{4}environment:\s*$/.test(line)) { inEnvironment = true; continue; }
    if (/^\s{4}(command|entrypoint):\s*/.test(line)) { inEnvironment = false; const value = line.replace(/^\s{4}(?:command|entrypoint):\s*/, ""); command = value.startsWith("[") ? (JSON.parse(value) as unknown[]).filter((part): part is string => typeof part === "string") : splitCommand(yamlScalar(value)); continue; }
    if (/^\s{4}(?:ports):\s*$/.test(line)) { inEnvironment = false; continue; }
    if (/^\s{6}-\s*/.test(line) && !inEnvironment) { const mapping = yamlScalar(line.replace(/^\s{6}-\s*/, "")); const candidate = Number(mapping.split(":").at(-1)); if (Number.isInteger(candidate)) port = candidate; continue; }
    if (inEnvironment) {
      const match = line.match(/^\s{6}([A-Za-z_][A-Za-z0-9_]*)\s*:\s*(.*)$/);
      const key = match?.[1];
      const value = match?.[2];
      if (key && value !== undefined) environment[key] = yamlScalar(value);
    }
  }
  if (!command.length && !Object.keys(environment).length && !port) throw new Error("No Compose service configuration found");
  return { command, environment, port: inferPort(command, environment, port), entryPath: "/", source: "compose service" };
}

export function parseApplicationImport(format: ApplicationImportFormat, text: string): ApplicationImportResult {
  if (!text.trim()) throw new Error("Import content is empty");
  if (format === "package") return packageImport(text);
  if (format === "procfile") return procfileImport(text);
  return composeImport(text);
}
