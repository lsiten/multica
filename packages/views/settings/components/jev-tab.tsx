"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, parseDaemonJevModel, parseDaemonJevModels, type DaemonJevModels, type DaemonJevModel } from "@multica/core/api";
import { useCurrentMember } from "@multica/core/permissions";
import { useCurrentWorkspace } from "@multica/core/paths";
import type { JevSource, WorkspaceJevConfig } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";
import { SettingsSection, SettingsTab } from "./settings-layout";

const LOCAL_MODEL = "Mapika/decider-2b";
const LOCAL_REVISION = "533964dae8be954c5b5e19fa4948e48408094c1e";
const ACTIVE_INSTALL_STATES = new Set(["accepted", "pending", "queued", "downloading", "verifying", "installing"]);

type DaemonJevAPI = {
  getJevModels?: () => Promise<unknown>;
  registerJevModel?: (id: string, revision: string) => Promise<unknown>;
  installJevModel?: (id: string, revision?: string) => Promise<unknown>;
  cancelJevModelInstall?: (id: string, revision?: string) => Promise<unknown>;
};

function daemonJevAPI(): DaemonJevAPI | undefined {
  if (typeof window === "undefined") return undefined;
  return (window as unknown as { daemonAPI?: DaemonJevAPI }).daemonAPI;
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
  const [modelBusy, setModelBusy] = useState(false);
  const [repository,setRepository]=useState("");
  const [repositoryRevision,setRepositoryRevision]=useState("main");
  const daemon = useMemo(() => daemonJevAPI(), []);
  const modelKey=["daemon-jev-models",workspaceID];
  const modelQuery=useQuery({queryKey:modelKey,enabled:draft?.source==="local"&&Boolean(daemon?.getJevModels),queryFn:async()=>{
    const next=parseDaemonJevModels(await daemon?.getJevModels?.());
    const previous=queryClient.getQueryData<DaemonJevModels>(modelKey);
    return {...next,status:next.status.length?next.status:previous?.status.filter(item=>ACTIVE_INSTALL_STATES.has(item.state))??[]};
  },refetchInterval:5_000});
  const modelData=modelQuery.data;

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
  }, [base, draft, draftWorkspaceID, query.data, workspaceID]);

  const source = draft?.source as JevSource | undefined;
  const selectedModelID=draft?.model_id??LOCAL_MODEL;
  const selectedRevision=draft?.model_revision??LOCAL_REVISION;
  const currentStatus = modelData?.status.find((item) => item.model_id === selectedModelID&&(!item.revision||item.revision===selectedRevision));
  const models=useMemo<DaemonJevModel[]>(()=>{
    const available=modelData?.models??[];
    if(available.some(model=>model.id===selectedModelID&&model.revision===selectedRevision))return available;
    return [{id:selectedModelID,revision:selectedRevision,devices:["auto","cpu","mps","cuda"]},...available];
  },[modelData,selectedModelID,selectedRevision]);
  const selectedModel=models.find(model=>model.id===selectedModelID&&model.revision===selectedRevision);
  const installActive = Boolean(currentStatus?.state && ACTIVE_INSTALL_STATES.has(currentStatus.state));
  const installed = currentStatus?.installed === true || (currentStatus?.installed === undefined && ["installed","ready","starting","stopped"].includes(currentStatus?.state??""));
  const statusLabels:Record<string,string>={not_installed:t($=>$.jev.not_installed),installed:t($=>$.jev.downloaded),ready:t($=>$.jev.ready),queued:t($=>$.jev.starting_download),accepted:t($=>$.jev.starting_download),starting:t($=>$.jev.model_starting),downloading:t($=>$.jev.model_downloading),verifying:t($=>$.jev.model_verifying),failed:t($=>$.jev.model_failed),stopped:t($=>$.jev.model_stopped)};
  const progress = currentStatus?.total_bytes && currentStatus.total_bytes > 0
    ? Math.min(100, Math.round(((currentStatus.downloaded_bytes ?? 0) / currentStatus.total_bytes) * 100))
    : undefined;
  const dirty = Boolean(draft && base && !configEqual(draft, base));

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
    return <SettingsTab title={t(($) => $.page.tabs.jev)} scope="workspace"><div className="space-y-2"><p role="alert" className="text-caption text-destructive">{t(($) => $.jev.load_failed)}</p>{query.error instanceof Error && <code className="block max-w-xl break-words rounded-xs bg-muted p-2 text-[11px]">{query.error.message}</code>}<Button size="sm" variant="outline" onClick={() => void query.refetch()}>{t(($) => $.jev.retry)}</Button></div></SettingsTab>;
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
      const accepted = await daemon.installJevModel(selectedModelID,selectedRevision);
      if (accepted && typeof accepted === "object" && "accepted" in accepted && (accepted as { accepted?: unknown }).accepted === true) {
        queryClient.setQueryData<DaemonJevModels>(modelKey,(previous)=>({models:previous?.models??[],status:[{model_id:selectedModelID,revision:selectedRevision,state:"accepted",phase:"queued"},...(previous?.status??[]).filter(item=>item.model_id!==selectedModelID||item.revision!==selectedRevision)]}));
      }
      await modelQuery.refetch();
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
      await daemon.cancelJevModelInstall(selectedModelID,selectedRevision);
      await modelQuery.refetch();
    } catch (error) {
      setDaemonError(error instanceof Error ? error.message : t(($) => $.jev.cancel_failed));
    } finally {
      setModelBusy(false);
    }
  };

  const registerModel=async()=>{
    if(!daemon?.registerJevModel){setDaemonError(t($=>$.jev.daemon_unavailable));return}
    setModelBusy(true);setDaemonError("");
    try{
      const model=parseDaemonJevModel(await daemon.registerJevModel(repository.trim(),repositoryRevision.trim()||"main"));
      if(activeWorkspaceID.current!==workspaceID)return;
      update({model_id:model.id,model_revision:model.revision});
      await modelQuery.refetch();setRepository("");
    }catch(error){setDaemonError(error instanceof Error?error.message:t($=>$.jev.daemon_failed))}
    finally{setModelBusy(false)}
  };

  return (
    <SettingsTab title={t(($) => $.page.tabs.jev)} description={t(($) => $.jev.description)} scope="workspace">
      <fieldset disabled={save.isPending} className="max-w-2xl space-y-6">
        <SettingsSection title={t(($) => $.jev.source_title)} description={t(($) => $.jev.source_description)}>
          <label className="block text-caption">
            <span className="mb-1 block text-muted-foreground">{t(($) => $.jev.source_label)}</span>
            <select aria-label={t(($) => $.jev.source_label)} className="h-9 w-full rounded-md border bg-background px-2" disabled={modelBusy} value={source} onChange={(event) => selectSource(event.target.value as JevSource)}>
              <option value="agent_context">{t(($) => $.jev.agent_context)}</option>
              <option value="local">{t(($) => $.jev.local)}</option>
              <option value="remote">{t(($) => $.jev.remote)}</option>
            </select>
          </label>
        </SettingsSection>

        {source === "local" && (
          <SettingsSection title={t(($) => $.jev.local_title)} description={t(($) => $.jev.local_description)}>
            <div className="mb-3 space-y-3">
              <label className="block text-caption"><span className="mb-1 block text-muted-foreground">{t($=>$.jev.choose_model)}</span><select aria-label={t($=>$.jev.choose_model)} className="h-9 w-full rounded-md border bg-background px-2" disabled={modelBusy} value={`${selectedModelID}@${selectedRevision}`} onChange={event=>{const model=models.find(item=>`${item.id}@${item.revision}`===event.target.value);if(model)update({model_id:model.id,model_revision:model.revision});setDaemonError("")}}>{models.map(model=><option key={`${model.id}@${model.revision}`} value={`${model.id}@${model.revision}`}>{model.id} · {model.revision.slice(0,8)}</option>)}</select></label>
              <details className="rounded-md border p-3"><summary className="cursor-pointer text-caption font-medium">{t($=>$.jev.add_huggingface)}</summary><div className="mt-3 space-y-3"><p className="text-caption text-muted-foreground">{t($=>$.jev.compatibility)}</p><label className="block text-caption">{t($=>$.jev.repository)}<input aria-label={t($=>$.jev.repository)} className="mt-1 h-9 w-full rounded-md border bg-background px-2" value={repository} disabled={modelBusy} onChange={event=>setRepository(event.target.value)}/></label><label className="block text-caption">{t($=>$.jev.repository_revision)}<input aria-label={t($=>$.jev.repository_revision)} className="mt-1 h-9 w-full rounded-md border bg-background px-2" value={repositoryRevision} disabled={modelBusy} onChange={event=>setRepositoryRevision(event.target.value)}/></label><Button size="sm" variant="outline" onClick={()=>void registerModel()} disabled={modelBusy||!repository.trim()||!daemon?.registerJevModel}>{modelBusy?t($=>$.jev.loading):t($=>$.jev.check_add)}</Button><p className="text-caption text-muted-foreground">{t($=>$.jev.metadata_only)}</p></div></details>
              <label className="block text-caption">{t($=>$.jev.device)}<select aria-label={t($=>$.jev.device)} className="ml-2 h-9 rounded-md border bg-background px-2" value={draft.device??"auto"} disabled={modelBusy} onChange={event=>update({device:event.target.value as WorkspaceJevConfig["device"]})}>{(selectedModel?.devices??["auto","cpu","mps","cuda"]).map(device=><option key={device} value={device}>{device}</option>)}</select></label>
            </div>
            <div className="space-y-3 rounded-md border bg-muted/30 p-3 text-caption">
              <div className="flex flex-wrap items-center justify-between gap-2"><div className="min-w-0"><div className="break-all font-medium">{selectedModelID}</div><div className="break-all text-caption text-muted-foreground">{t(($) => $.jev.local_revision, { revision: selectedRevision })}</div></div><span aria-live="polite" className="text-muted-foreground">{statusLabels[currentStatus?.state??"not_installed"]??currentStatus?.state}</span></div>
              {progress !== undefined && <div aria-label={t(($) => $.jev.download_progress, { progress })} className="h-2 overflow-hidden rounded-full bg-muted"><div className="h-full bg-primary transition-[width]" style={{ width: `${progress}%` }} /></div>}
              {currentStatus?.error && <p role="alert" className="text-destructive">{currentStatus.error}</p>}
              {daemonError && <p role="alert" className="text-destructive">{daemonError}</p>}
              {modelQuery.isError&&<p role="alert" className="text-destructive">{modelQuery.error.message}</p>}
              <div className="flex flex-wrap gap-2"><Button size="sm" variant="outline" onClick={() => void modelQuery.refetch()} disabled={modelBusy||!daemon?.getJevModels}>{t(($) => $.jev.check_daemon)}</Button>{installActive ? <Button size="sm" variant="outline" onClick={() => void cancel()} disabled={modelBusy}>{t(($) => $.jev.cancel_download)}</Button> : <Button size="sm" onClick={() => void install()} disabled={modelBusy || installed || !daemon?.installJevModel}>{modelBusy ? t(($) => $.jev.starting_download) : installed ? t(($) => $.jev.downloaded) : t(($) => $.jev.confirm_download)}</Button>}</div>
              {selectedModel?.license&&<p className="text-caption text-muted-foreground">{t($=>$.jev.license)}: {selectedModel.license}</p>}
              {selectedModel?.download_bytes!==undefined&&<p className="text-caption text-muted-foreground">{t($=>$.jev.download_size)}: {formatBytes(selectedModel.download_bytes)}</p>}
              {currentStatus&&<p className="text-caption text-muted-foreground">{t($=>$.jev.model_service,{device:currentStatus.device??"—",leases:currentStatus.active_leases??0})}</p>}
              {!daemon && <p className="text-muted-foreground">{t(($) => $.jev.web_unavailable)}</p>}
              {currentStatus?.downloaded_bytes !== undefined && currentStatus.total_bytes !== undefined && <p className="text-[11px] text-muted-foreground">{formatBytes(currentStatus.downloaded_bytes)} / {formatBytes(currentStatus.total_bytes)}</p>}
            </div>
          </SettingsSection>
        )}

        {source === "remote" && <SettingsSection title={t(($) => $.jev.remote_title)}><div className="space-y-3"><label className="block text-caption"><span className="mb-1 block text-muted-foreground">{t(($) => $.jev.endpoint)}</span><input aria-label={t(($) => $.jev.endpoint)} className="h-9 w-full rounded-md border bg-background px-2" value={draft.endpoint ?? ""} onChange={(event) => update({ endpoint: event.target.value })} placeholder={t(($) => $.jev.endpoint_placeholder)} /></label><label className="block text-caption"><span className="mb-1 block text-muted-foreground">{t(($) => $.jev.model_id)}</span><input aria-label={t(($) => $.jev.model_id)} className="h-9 w-full rounded-md border bg-background px-2" value={draft.model_id ?? ""} onChange={(event) => update({ model_id: event.target.value })} /></label><label className="block text-caption"><span className="mb-1 block text-muted-foreground">{t(($) => $.jev.credential_env)}</span><input aria-label={t(($) => $.jev.credential_env)} className="h-9 w-full rounded-md border bg-background px-2" value={draft.credential_env ?? ""} onChange={(event) => update({ credential_env: event.target.value })} placeholder={t(($) => $.jev.credential_env_placeholder)} /></label></div></SettingsSection>}

        <label className="block text-caption"><span className="mb-1 block text-muted-foreground">{t(($) => $.jev.timeout)}</span><input aria-label={t(($) => $.jev.timeout)} type="number" min={1} max={300} className="h-9 w-40 rounded-md border bg-background px-2" value={draft.timeout_seconds} onChange={(event) => update({ timeout_seconds: Number(event.target.value) || 0 })} disabled={source === "agent_context"} /></label>
        {validationError && <p role="alert" className="text-caption text-destructive">{validationError}</p>}
        <div className="flex justify-end"><Button size="sm" onClick={saveConfig} disabled={!dirty || save.isPending||modelBusy}>{save.isPending ? t(($) => $.jev.saving) : t(($) => $.jev.save)}</Button></div>
      </fieldset>
    </SettingsTab>
  );
}
