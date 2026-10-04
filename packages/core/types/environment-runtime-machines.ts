import type { EnvironmentRuntime } from "./environment-operations";

export interface EnvironmentRuntimeMachine {
  id: string;
  daemonId: string | null;
  title: string;
  runtimes: EnvironmentRuntime[];
}

// Names are display values. Only the daemon identity can join runtime scopes.
export function groupEnvironmentRuntimeMachines(runtimes: EnvironmentRuntime[]): EnvironmentRuntimeMachine[] {
  const groups = new Map<string, EnvironmentRuntimeMachine>();
  for (const runtime of runtimes) {
    const id = JSON.stringify([runtime.workspace_id, runtime.owner_id, runtime.runtime_mode, runtime.daemon_id ? ["daemon", runtime.daemon_id] : ["runtime", runtime.id]]);
    const machine = groups.get(id) ?? { id, daemonId: runtime.daemon_id, title: "", runtimes: [] };
    machine.runtimes.push(runtime);
    groups.set(id, machine);
  }
  return [...groups.values()].map((machine) => {
    machine.runtimes.sort((a, b) => Number(b.status === "online") - Number(a.status === "online") || a.provider.localeCompare(b.provider) || a.id.localeCompare(b.id));
    const first = machine.runtimes[0]!;
    const custom = first.custom_name?.trim();
    machine.title = custom && machine.runtimes.every((runtime) => runtime.custom_name?.trim() === custom)
      ? custom : first.name.match(/\(([^)]+)\)$/)?.[1] || first.name;
    return machine;
  }).sort((a, b) => Number(b.runtimes.some((runtime) => runtime.status === "online")) - Number(a.runtimes.some((runtime) => runtime.status === "online")) || a.title.localeCompare(b.title) || a.id.localeCompare(b.id));
}

export function environmentRuntimeLabel(runtime: EnvironmentRuntime, machineTitle: string): string {
  const custom = runtime.custom_name?.trim();
  return custom && custom !== machineTitle ? custom : runtime.provider === "unknown" ? runtime.name : runtime.provider;
}

export function environmentRuntimeAPIURL(value: string | undefined): string | null {
  if (!value) return null;
  try {
    const url = new URL(value);
    if (!["http:", "https:"].includes(url.protocol)) return null;
    url.username = ""; url.password = ""; url.search = ""; url.hash = "";
    return url.toString().replace(/\/$/, "");
  } catch {
    return null;
  }
}
