"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { z } from "zod";
import { api } from "@multica/core/api";
import { useCurrentMember } from "@multica/core/permissions";
import { useCurrentWorkspace } from "@multica/core/paths";
import type { JevSource, WorkspaceJevConfig } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";
import { SettingsSection, SettingsTab } from "./settings-layout";

const LOCAL_MODEL = "Mapika/decider-2b";
const LOCAL_REVISION = "533964dae8be954c5b5e19fa4948e48408094c1e";
const ACTIVE_INSTALL_STATES = new Set(["accepted", "pending", "queued", "downloading", "verifying", "installing"]);

type DaemonModel = {
  id?: string;
  revision?: string;
  download_bytes?: number;
  devices?: string[];
};
type DaemonModelStatus = {
  model_id?: string;
  state?: string;
  phase?: string;
  downloaded_bytes?: number;
  total_bytes?: number;
  error?: string;
};
type DaemonJevResponse = { models?: DaemonModel[]; status?: DaemonModelStatus[] };
const DaemonJevResponseSchema = z.object({
  models: z.array(z.object({ id: z.string().optional(), revision: z.string().optional(), download_bytes: z.number().nonnegative().optional(), devices: z.array(z.string()).optional() }).passthrough()).optional(),
  status: z.array(z.object({ model_id: z.string().optional(), state: z.string().optional(), phase: z.string().optional(), downloaded_bytes: z.number().nonnegative().optional(), total_bytes: z.number().nonnegative().optional(), error: z.string().optional() }).passthrough()).optional(),
}).passthrough();
type DaemonJevAPI = {
  getJevModels?: () => Promise<unknown>;
  installJevModel?: (id: string) => Promise<unknown>;
  cancelJevModelInstall?: (id: string) => Promise<unknown>;
};

function daemonJevAPI(): DaemonJevAPI | undefined {
  if (typeof window === "undefined") return undefined;
  return (window as unknown as { daemonAPI?: DaemonJevAPI }).daemonAPI;
}

function readDaemonResponse(value: unknown): DaemonJevResponse | null {
  const parsed = DaemonJevResponseSchema.safeParse(value);
  return parsed.success ? parsed.data : null;
}

function formatBytes(bytes: number | undefined): string {
  if (!bytes || bytes < 0) return "";
  if (bytes < 1024 ** 2) return `${Math.round(bytes / 1024)} KB`;
  if (bytes < 1024 ** 3) return `${(bytes / 1024 ** 2).toFixed(1)} MB`;
  return `${(bytes / 1024 ** 3).toFixed(2)} GB`;
}

function configEqual(left: WorkspaceJevConfig, right: WorkspaceJevConfig): boolean {
  return JSON.stringify(left) === JSON.stringify(right);
}

export function JevTab() {
  const { t } = useT("settings");
  const workspace = useCurrentWorkspace();
  const workspaceID = workspace?.id ?? "";
  const member = useCurrentMember(workspaceID);
  const canManage = member.role === "owner" || member.role === "admin";
  const queryClient = useQueryClient();
  const query = useQuery({
    queryKey: ["workspace-jev-config", workspaceID],
    enabled: Boolean(workspaceID),
    queryFn: () => api.getWorkspaceJevConfig(workspaceID),
  });
  const [draft, setDraft] = useState<WorkspaceJevConfig | null>(null);
  const [base, setBase] = useState<WorkspaceJevConfig | null>(null);
  const activeWorkspaceID = useRef(workspaceID);
  if (activeWorkspaceID.current !== workspaceID) activeWorkspaceID.current = workspaceID;
  const [draftWorkspaceID, setDraftWorkspaceID] = useState("");
  const [validationError, setValidationError] = useState("");
  const [daemonError, setDaemonError] = useState("");
  const [modelData, setModelData] = useState<DaemonJevResponse>({});
  const [modelBusy, setModelBusy] = useState(false);
  const daemon = useMemo(() => daemonJevAPI(), []);
  const pollTimer = useRef<ReturnType<typeof setInterval> | undefined>(undefined);

  const loadModelStatus = useCallback(async () => {
    if (!daemon?.getJevModels) {
      setDaemonError(t(($) => $.jev.daemon_unavailable));
      return;
    }
    try {
      const next = readDaemonResponse(await daemon.getJevModels());
      if (!next) {
        setDaemonError(t(($) => $.jev.daemon_invalid));
        return;
      }
      setModelData((previous) => ({
        ...next,
        // An install request may be acknowledged before the daemon has
        // persisted its first progress snapshot. Keep the accepted state so
        // the UI remains cancellable instead of pretending the download ended.
        status: next.status?.length ? next.status : previous.status,
      }));
      setDaemonError("");
    } catch (error) {
      setDaemonError(error instanceof Error ? error.message : t(($) => $.jev.daemon_failed));
    }
  }, [daemon, t]);

  useEffect(() => {
    if (draftWorkspaceID && draftWorkspaceID !== workspaceID) {
      setDraft(null);
      setBase(null);
      setDraftWorkspaceID("");
      setValidationError("");
    }
  }, [draftWorkspaceID, workspaceID]);

  useEffect(() => {
    const response = query.data;
    const config = response?.config;
    if (!config || !workspaceID || response.workspace_id !== workspaceID) return;
    // A refetch may be caused by another tab. Never replace unsaved local edits.
    if (draftWorkspaceID !== workspaceID || !draft || !base || configEqual(draft, base)) {
      setDraft(config);
      setBase(config);
      setDraftWorkspaceID(workspaceID);
      setValidationError("");
    }
  }, [base, draft, draftWorkspaceID, query.data?.config, workspaceID]);

  const source = draft?.source as JevSource | undefined;
  const currentStatus = modelData.status?.find((item) => item.model_id === LOCAL_MODEL);
  const installActive = Boolean(currentStatus?.state && ACTIVE_INSTALL_STATES.has(currentStatus.state));
  const installed = currentStatus?.state === "installed" || currentStatus?.state === "ready";
  const progress = currentStatus?.total_bytes && currentStatus.total_bytes > 0
    ? Math.min(100, Math.round(((currentStatus.downloaded_bytes ?? 0) / currentStatus.total_bytes) * 100))
    : undefined;
  const dirty = Boolean(draft && base && !configEqual(draft, base));

  useEffect(() => {
    if (source !== "local") return;
    void loadModelStatus();
  }, [loadModelStatus, source]);

  useEffect(() => {
    if (!installActive) return;
    pollTimer.current = setInterval(() => void loadModelStatus(), 1000);
    return () => {
      if (pollTimer.current) clearInterval(pollTimer.current);
    };
  }, [installActive, loadModelStatus]);

  const save = useMutation({
    mutationFn: (input: { config: WorkspaceJevConfig; revision: number }) =>
      api.updateWorkspaceJevConfig(workspaceID, input),
    onSuccess: (value) => {
      if (activeWorkspaceID.current !== workspaceID) return;
      setDraft(value.config);
      setBase(value.config);
      setValidationError("");
      queryClient.setQueryData(["workspace-jev-config", workspaceID], value);
    },
    onError: (error) => {
      if (activeWorkspaceID.current !== workspaceID) return;
      const status = typeof error === "object" && error !== null && "status" in error ? (error as { status?: unknown }).status : undefined;
      setValidationError(status === 409 ? t(($) => $.jev.conflict) : error instanceof Error ? error.message : t(($) => $.jev.save_failed));
    },
  });

  if (!canManage) {
    return <SettingsTab title={t(($) => $.page.tabs.jev)} scope="workspace"><p className="text-caption text-muted-foreground">{t(($) => $.jev.admin_only)}</p></SettingsTab>;
  }
  if (query.isError) {
    return <SettingsTab title={t(($) => $.page.tabs.jev)} scope="workspace"><div className="space-y-2"><p role="alert" className="text-caption text-destructive">{t(($) => $.jev.load_failed)}</p>{query.error instanceof Error && <code className="block max-w-xl break-words rounded bg-muted p-2 text-[11px]">{query.error.message}</code>}<Button size="sm" variant="outline" onClick={() => void query.refetch()}>{t(($) => $.jev.retry)}</Button></div></SettingsTab>;
  }
  if (query.isLoading || !draft || draftWorkspaceID !== workspaceID) {
    return <SettingsTab title={t(($) => $.page.tabs.jev)} scope="workspace"><p className="text-caption text-muted-foreground">{t(($) => $.jev.loading)}</p></SettingsTab>;
  }

  const update = (patch: Partial<WorkspaceJevConfig>) => {
    setDraft((current) => current ? { ...current, ...patch } : current);
    setValidationError("");
  };
  const selectSource = (next: JevSource) => {
    if (next === "local") {
      update({ source: next, model_id: LOCAL_MODEL, model_revision: LOCAL_REVISION, device: "auto", endpoint: undefined, credential_env: undefined });
    } else if (next === "agent_context") {
      update({ source: next, model_id: undefined, model_revision: undefined, device: undefined, endpoint: undefined, credential_env: undefined });
    } else {
      update({ source: next, model_id: draft.model_id ?? "", model_revision: undefined, device: undefined });
    }
  };
  const saveConfig = () => {
    if (source === "remote" && (!draft.endpoint?.startsWith("https://") || !draft.model_id?.trim())) {
      setValidationError(t(($) => $.jev.remote_invalid));
      return;
    }
    if (draft.timeout_seconds < 1 || draft.timeout_seconds > 300) {
      setValidationError(t(($) => $.jev.timeout_invalid));
      return;
    }
    save.mutate({ revision: base?.revision ?? draft.revision ?? 0, config: { ...draft, revision: 0 } });
  };
  const install = async () => {
    if (!daemon?.installJevModel) {
      setDaemonError(t(($) => $.jev.daemon_unavailable));
      return;
    }
    setDaemonError("");
    setModelBusy(true);
    try {
      const accepted = await daemon.installJevModel(LOCAL_MODEL);
      if (accepted && typeof accepted === "object" && "accepted" in accepted && (accepted as { accepted?: unknown }).accepted === true) {
        setModelData((previous) => ({
          ...previous,
          status: previous.status?.some((item) => item.model_id === LOCAL_MODEL)
            ? previous.status
            : [{ model_id: LOCAL_MODEL, state: "accepted", phase: "queued" }, ...(previous.status ?? [])],
        }));
      }
      await loadModelStatus();
    } catch (error) {
      setDaemonError(error instanceof Error ? error.message : t(($) => $.jev.install_failed));
    } finally {
      setModelBusy(false);
    }
  };
  const cancel = async () => {
    if (!daemon?.cancelJevModelInstall) return;
    setModelBusy(true);
    try {
      await daemon.cancelJevModelInstall(LOCAL_MODEL);
      await loadModelStatus();
    } catch (error) {
      setDaemonError(error instanceof Error ? error.message : t(($) => $.jev.cancel_failed));
    } finally {
      setModelBusy(false);
    }
  };

  return (
    <SettingsTab title={t(($) => $.page.tabs.jev)} description={t(($) => $.jev.description)} scope="workspace">
      <div className="max-w-2xl space-y-6">
        <SettingsSection title={t(($) => $.jev.source_title)} description={t(($) => $.jev.source_description)}>
          <label className="block text-caption">
            <span className="mb-1 block text-muted-foreground">{t(($) => $.jev.source_label)}</span>
            <select aria-label={t(($) => $.jev.source_label)} className="h-9 w-full rounded-md border bg-background px-2" value={source} onChange={(event) => selectSource(event.target.value as JevSource)}>
              <option value="agent_context">{t(($) => $.jev.agent_context)}</option>
              <option value="local">{t(($) => $.jev.local)}</option>
              <option value="remote">{t(($) => $.jev.remote)}</option>
            </select>
          </label>
        </SettingsSection>

        {source === "local" && (
          <SettingsSection title={t(($) => $.jev.local_title)} description={t(($) => $.jev.local_description)}>
            <div className="space-y-3 rounded-md border bg-muted/30 p-3 text-caption">
              <div className="flex flex-wrap items-center justify-between gap-2"><div><div className="font-medium">{LOCAL_MODEL}</div><div className="text-[11px] text-muted-foreground">{t(($) => $.jev.local_revision, { revision: LOCAL_REVISION })}</div></div><span aria-live="polite" className="text-muted-foreground">{installed ? t(($) => $.jev.ready) : currentStatus?.state ?? t(($) => $.jev.not_installed)}</span></div>
              {progress !== undefined && <div aria-label={t(($) => $.jev.download_progress, { progress })} className="h-2 overflow-hidden rounded-full bg-muted"><div className="h-full bg-primary transition-[width]" style={{ width: `${progress}%` }} /></div>}
              {currentStatus?.error && <p role="alert" className="text-destructive">{currentStatus.error}</p>}
              {daemonError && <p role="alert" className="text-destructive">{daemonError}</p>}
              <div className="flex flex-wrap gap-2"><Button size="sm" variant="outline" onClick={() => void loadModelStatus()} disabled={modelBusy}>{t(($) => $.jev.check_daemon)}</Button>{installActive ? <Button size="sm" variant="outline" onClick={() => void cancel()} disabled={modelBusy}>{t(($) => $.jev.cancel_download)}</Button> : <Button size="sm" onClick={() => void install()} disabled={modelBusy || installed || !daemon?.installJevModel}>{modelBusy ? t(($) => $.jev.starting_download) : installed ? t(($) => $.jev.ready) : t(($) => $.jev.confirm_download)}</Button>}</div>
              {!daemon && <p className="text-muted-foreground">{t(($) => $.jev.web_unavailable)}</p>}
              {currentStatus?.downloaded_bytes !== undefined && currentStatus.total_bytes !== undefined && <p className="text-[11px] text-muted-foreground">{formatBytes(currentStatus.downloaded_bytes)} / {formatBytes(currentStatus.total_bytes)}</p>}
            </div>
          </SettingsSection>
        )}

        {source === "remote" && <SettingsSection title={t(($) => $.jev.remote_title)}><div className="space-y-3"><label className="block text-caption"><span className="mb-1 block text-muted-foreground">{t(($) => $.jev.endpoint)}</span><input aria-label={t(($) => $.jev.endpoint)} className="h-9 w-full rounded-md border bg-background px-2" value={draft.endpoint ?? ""} onChange={(event) => update({ endpoint: event.target.value })} placeholder={t(($) => $.jev.endpoint_placeholder)} /></label><label className="block text-caption"><span className="mb-1 block text-muted-foreground">{t(($) => $.jev.model_id)}</span><input aria-label={t(($) => $.jev.model_id)} className="h-9 w-full rounded-md border bg-background px-2" value={draft.model_id ?? ""} onChange={(event) => update({ model_id: event.target.value })} /></label><label className="block text-caption"><span className="mb-1 block text-muted-foreground">{t(($) => $.jev.credential_env)}</span><input aria-label={t(($) => $.jev.credential_env)} className="h-9 w-full rounded-md border bg-background px-2" value={draft.credential_env ?? ""} onChange={(event) => update({ credential_env: event.target.value })} placeholder={t(($) => $.jev.credential_env_placeholder)} /></label></div></SettingsSection>}

        <label className="block text-caption"><span className="mb-1 block text-muted-foreground">{t(($) => $.jev.timeout)}</span><input aria-label={t(($) => $.jev.timeout)} type="number" min={1} max={300} className="h-9 w-40 rounded-md border bg-background px-2" value={draft.timeout_seconds} onChange={(event) => update({ timeout_seconds: Number(event.target.value) || 0 })} disabled={source === "agent_context"} /></label>
        {validationError && <p role="alert" className="text-caption text-destructive">{validationError}</p>}
        <div className="flex justify-end"><Button size="sm" onClick={saveConfig} disabled={!dirty || save.isPending}>{save.isPending ? t(($) => $.jev.saving) : t(($) => $.jev.save)}</Button></div>
      </div>
    </SettingsTab>
  );
}
