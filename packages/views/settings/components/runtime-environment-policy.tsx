import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "@multica/core/api";
import { environmentPolicySchema, type EnvironmentPolicyStatus } from "@multica/core/types/environment-operations";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { useT } from "../../i18n";

export function RuntimeEnvironmentPolicy({ workspaceId, runtimeId, userId, disabled }: { workspaceId: string; runtimeId: string; userId: string; disabled: boolean }) {
  const { t } = useT("settings");
  const client = useQueryClient();
  const [open, setOpen] = useState(false);
  const key = ["workspaces", workspaceId, "runtime-environments", userId, runtimeId, "policy"];
  const query = useQuery({ queryKey: key, queryFn: () => api.executeRuntimeEnvironment(workspaceId, runtimeId, { action: "policy" }), retry: false, refetchInterval: open ? 10000 : false });
  const save = useMutation({
    mutationFn: (data: FormData) => api.executeRuntimeEnvironment(workspaceId, runtimeId, { action: "policy_update", policy: environmentPolicySchema.parse({
      enabled: data.get("enabled") === "on", archive_after_hours: query.data?.task_retention_supported ? 0 : Number(data.get("archive")), cache_after_hours: Number(data.get("cache")),
      pressure_cache_after_hours: Number(data.get("pressure_cache")), max_idle_environments: Number(data.get("count")),
      max_directory_bytes: Math.round(Number(data.get("size")) * 1024 ** 3), minimum_free_bytes: Math.round(Number(data.get("free")) * 1024 ** 3),
    }) }),
    onSuccess: (status) => client.setQueryData(key, status),
  });
  const status = query.data;
  const error = query.error ?? save.error;
  return <details className="rounded-lg border p-3" open={open} onToggle={(event) => setOpen(event.currentTarget.open)}>
    <summary className="cursor-pointer text-body font-medium">{t(($) => $.environments.policy.title)}</summary>
    {error && <p role="alert" className="my-2 break-words text-body text-destructive">{error.message}</p>}
    {status && <PolicyForm key={JSON.stringify(status.policy)} status={status} disabled={disabled || save.isPending} onSubmit={(data) => save.mutate(data)} />}
    {save.isSuccess && <p role="status" className="mt-2 text-caption">{t(($) => $.environments.policy.saved)}</p>}
  </details>;
}

function PolicyForm({ status, disabled, onSubmit }: { status: EnvironmentPolicyStatus; disabled: boolean; onSubmit: (data: FormData) => void }) {
  const { t } = useT("settings");
  const fields = [
    ...(status.task_retention_supported ? [] : [{ name: "archive", label: t(($) => $.environments.policy.archive_hours), value: status.policy.archive_after_hours, max: 87600, step: 1 }]),
    { name: "cache", label: t(($) => $.environments.policy.cache_hours), value: status.policy.cache_after_hours, max: 87600, step: 1 },
    { name: "pressure_cache", label: t(($) => $.environments.policy.pressure_cache_hours), value: status.policy.pressure_cache_after_hours, max: 87600, step: 1 },
    { name: "count", label: t(($) => $.environments.policy.max_count), value: status.policy.max_idle_environments, max: 100000, step: 1 },
    { name: "size", label: t(($) => $.environments.policy.max_size), value: status.policy.max_directory_bytes / 1024 ** 3, max: 1048576, step: "any" },
    { name: "free", label: t(($) => $.environments.policy.min_free), value: status.policy.minimum_free_bytes / 1024 ** 3, max: 1048576, step: "any" },
  ];
  return <form className="mt-3 space-y-4" onSubmit={(event) => { event.preventDefault(); onSubmit(new FormData(event.currentTarget)); }}>
    <Label className="flex items-center gap-2"><input type="checkbox" name="enabled" defaultChecked={status.policy.enabled} disabled={disabled} />{t(($) => $.environments.policy.enabled)}</Label>
    {!status.effective_enabled && <p className="text-caption text-muted-foreground">{t(($) => $.environments.policy.inactive)}</p>}
    <p className="text-caption text-muted-foreground">{status.task_retention_supported ? t(($) => $.environments.policy.task_retention_help) : t(($) => $.environments.policy.retention_help)}</p>
    <div className="grid gap-3 sm:grid-cols-2">{fields.map((field) => <Label key={field.name} className="grid gap-1.5">{field.label}<Input name={field.name} type="number" required min={0} max={field.max} step={field.step} defaultValue={field.value} disabled={disabled} /></Label>)}</div>
    <p className="text-caption">{t(($) => $.environments.policy.pressure_status, { status: status.under_pressure ? t(($) => $.environments.policy.pressure) : t(($) => $.environments.policy.normal), free: status.free_bytes === null ? "—" : `${(status.free_bytes / 1024 ** 3).toFixed(1)} GiB` })}</p>
    <p className="text-caption text-muted-foreground">{t(($) => $.environments.policy.last_scan, { time: status.last_scan_at ? new Date(status.last_scan_at).toLocaleString() : "—", count: status.idle_environments })}</p>
    <Button size="sm" disabled={disabled} type="submit">{t(($) => $.environments.policy.save)}</Button>
  </form>;
}
