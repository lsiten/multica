"use client";

import type { EnvironmentRuntime } from "@multica/core/types/environment-operations";
import { environmentRuntimeAPIURL, environmentRuntimeLabel, groupEnvironmentRuntimeMachines } from "@multica/core/types/environment-runtime-machines";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@multica/ui/components/ui/select";
import { useT } from "../../i18n";

export function RuntimeEnvironmentPicker({ runtimes, runtimeId, disabled, onChange }: {
  runtimes: EnvironmentRuntime[];
  runtimeId: string;
  disabled: boolean;
  onChange: (id: string) => void;
}) {
  const { t } = useT("settings");
  const machines = groupEnvironmentRuntimeMachines(runtimes);
  const machine = machines.find((item) => item.runtimes.some((runtime) => runtime.id === runtimeId));
  const selected = machine?.runtimes.find((runtime) => runtime.id === runtimeId);
  const unknown = t(($) => $.environments.not_reported);
  const source = (runtime: EnvironmentRuntime) => runtime.metadata.launched_by === "desktop"
    ? t(($) => $.environments.desktop_source)
    : runtime.metadata.launched_by === "" || runtime.metadata.launched_by === "cli"
      ? t(($) => $.environments.standalone_source) : unknown;
  const status = (runtime: EnvironmentRuntime) => runtime.status === "online" ? t(($) => $.environments.online) : t(($) => $.environments.offline);
  const version = (runtime: EnvironmentRuntime) => runtime.metadata.cli_version?.trim() || unknown;
  const machineLabel = (item: typeof machines[number]) => `${item.title} · ${(item.daemonId || item.runtimes[0]!.id).slice(0, 8)}`;
  return <div className="min-w-0 space-y-3">
    <div className="flex flex-wrap items-end gap-3">
      <div className="min-w-0 space-y-1">
        <p className="text-caption text-muted-foreground">{t(($) => $.environments.choose_machine)}</p>
        <Select items={[{ value: "", label: t(($) => $.environments.choose_machine) }, ...machines.map((item) => ({ value: item.id, label: machineLabel(item) }))]}
          value={machine?.id || ""} disabled={disabled}
          onValueChange={(value) => onChange(machines.find((item) => item.id === value)?.runtimes[0]?.id || "")}>
          <SelectTrigger className="max-w-full" aria-label={t(($) => $.environments.choose_machine)}><SelectValue /></SelectTrigger>
          <SelectContent align="start" alignItemWithTrigger={false} className="w-auto max-w-[calc(100vw-2rem)]">
            <SelectItem value="">{t(($) => $.environments.choose_machine)}</SelectItem>
            {machines.map((item) => {
              const runtime = item.runtimes[0]!;
              return <SelectItem key={item.id} value={item.id} className="py-2">
                <div className="min-w-0 max-w-sm space-y-1 whitespace-normal">
                  <p className="break-words font-medium">{machineLabel(item)} · {status(runtime)}</p>
                  <p className="text-caption text-muted-foreground">{t(($) => $.environments.host_summary, { version: version(runtime), source: source(runtime) })}</p>
                  <p className="break-all text-caption text-muted-foreground">{t(($) => $.environments.api_endpoint, { address: environmentRuntimeAPIURL(runtime.metadata.server_url) || unknown })}</p>
                </div>
              </SelectItem>;
            })}
          </SelectContent>
        </Select>
      </div>
      {machine && <div className="min-w-0 space-y-1">
        <p className="text-caption text-muted-foreground">{t(($) => $.environments.choose_runtime)}</p>
        <Select items={machine.runtimes.map((runtime) => ({ value: runtime.id, label: environmentRuntimeLabel(runtime, machine.title) }))}
          value={runtimeId} disabled={disabled} onValueChange={(value) => { if (value) onChange(value); }}>
          <SelectTrigger className="max-w-full" aria-label={t(($) => $.environments.choose_runtime)}><SelectValue /></SelectTrigger>
          <SelectContent align="start" alignItemWithTrigger={false} className="w-auto max-w-[calc(100vw-2rem)]">
            {machine.runtimes.map((runtime) => <SelectItem key={runtime.id} value={runtime.id} className="py-2">
              <div className="min-w-0 max-w-sm space-y-1 whitespace-normal">
                <p className="break-words font-medium">{environmentRuntimeLabel(runtime, machine.title)} · {status(runtime)} · {runtime.id.slice(0, 8)}</p>
                <p className="text-caption text-muted-foreground">{t(($) => $.environments.agent_version)}: {runtime.metadata.version || unknown}</p>
                <p className="text-caption text-muted-foreground">{t(($) => $.environments.host_summary, { version: version(runtime), source: source(runtime) })}</p>
                <p className="break-all text-caption text-muted-foreground">{t(($) => $.environments.api_endpoint, { address: environmentRuntimeAPIURL(runtime.metadata.server_url) || unknown })}</p>
              </div>
            </SelectItem>)}
          </SelectContent>
        </Select>
      </div>}
    </div>
    {selected && machine && <dl className="grid min-w-0 gap-x-6 gap-y-2 rounded-lg border p-3 text-caption sm:grid-cols-2">
      <div><dt className="text-muted-foreground">{t(($) => $.environments.multica_version)}</dt><dd>{version(selected)} · {source(selected)}</dd></div>
      <div><dt className="text-muted-foreground">{t(($) => $.environments.agent_version)}</dt><dd className="break-words">{environmentRuntimeLabel(selected, machine.title)} · {selected.metadata.version || unknown} · {status(selected)}</dd></div>
      <div className="min-w-0 sm:col-span-2"><dt className="text-muted-foreground">{t(($) => $.environments.api_address)}</dt><dd className="break-all font-mono">{environmentRuntimeAPIURL(selected.metadata.server_url) || unknown}</dd></div>
      <div className="min-w-0 sm:col-span-2"><dt className="text-muted-foreground">{t(($) => $.environments.runtime_identity)}</dt><dd className="break-all font-mono">{selected.id}</dd></div>
      <div className="sm:col-span-2 text-muted-foreground">{t(($) => $.environments.selected_scope)}</div>
    </dl>}
  </div>;
}
