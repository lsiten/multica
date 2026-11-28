"use client";

import { useCallback, useEffect, useId, useState, type FormEvent, type ReactNode, type ReactElement } from "react";
import { useQuery } from "@tanstack/react-query";
import { Loader2, Plus, Trash2 } from "@multica/ui/icons";
import { toast } from "sonner";
import { applicationEnvironmentText, applicationListOptions, defaultApplicationConfig, parseApplicationArguments, parseApplicationEnvironment, parseApplicationImport, useCreateApplication, useUpdateApplication, type Application, type ApplicationConfig, type ApplicationImportFormat } from "@multica/core/applications";
import { projectListOptions, projectResourcesOptions } from "@multica/core/projects";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Switch } from "@multica/ui/components/ui/switch";
import { Dialog, DialogClose, DialogContent, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@multica/ui/components/ui/dialog";
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@multica/ui/components/ui/alert-dialog";
import { useT } from "../i18n";
import { AppLink, useNavigationGuard } from "../navigation";
import { useWorkspacePaths } from "@multica/core/paths";
import { runGuardedNavigation } from "@multica/core/platform";
import { confirmNavigation } from "../platform/navigation-confirmation";
import { ApplicationSelect } from "./controls";

function FormField({ label, children, help }: { label: string; children: ReactNode; help?: string }) {
  return <div className="space-y-1.5"><label className="block text-caption font-medium">{label}{children}</label>{help && <p className="text-caption text-muted-foreground">{help}</p>}</div>;
}

interface PreparationDraft { program: string; arguments: string; timeout: number }
interface EditorState { readonly dirty: boolean; readonly pending: boolean }

function EditorForm({ workspaceId, initial, projectId: presetProjectId, onSaved, onStateChange }: { workspaceId: string; initial?: Application; projectId?: string; onSaved: (application: Application) => void; onStateChange: (state: EditorState) => void }) {
  const { t } = useT("applications");
  const formId = useId();
  const paths = useWorkspacePaths();
  const [baseline] = useState(initial);
  const [name, setName] = useState(initial?.name ?? "");
  const [description, setDescription] = useState(initial?.description ?? "");
  const [projectDraft, setProjectId] = useState(initial?.project_id ?? presetProjectId ?? "");
  const [kind, setKind] = useState<"service" | "composition">(initial?.kind === "composition" ? "composition" : "service");
  const [template, setTemplate] = useState("custom");
  const [importFormat, setImportFormat] = useState<ApplicationImportFormat>("compose");
  const [importText, setImportText] = useState("");
  const [config, setConfig] = useState<ApplicationConfig>(() => initial?.config ?? defaultApplicationConfig());
  const [program, setProgram] = useState(initial?.config.command[0] ?? "");
  const [argumentItems, setArgumentItems] = useState<string[]>(initial?.config.command.slice(1) ?? []);
  const [argumentsText, setArgumentsText] = useState(JSON.stringify(initial?.config.command.slice(1) ?? []));
  const [environmentText, setEnvironmentText] = useState(applicationEnvironmentText(initial?.config.environment ?? {}));
  const [localEnvText, setLocalEnvText] = useState(applicationEnvironmentText(initial?.config.local_env ?? {}));
  const [prepare, setPrepare] = useState<PreparationDraft[]>(() => (initial?.config.prepare ?? []).map((step) => ({ program: step.args[0] ?? "", arguments: JSON.stringify(step.args.slice(1)), timeout: step.timeout_seconds })));
  const [error, setError] = useState("");
  const projects = useQuery(projectListOptions(workspaceId));
  const projectId = projectDraft || (projects.data?.length === 1 ? projects.data[0]?.id ?? "" : "");
  const resources = useQuery({ ...projectResourcesOptions(workspaceId, projectId), enabled: !!projectId && kind === "service" });
  const applications = useQuery({ ...applicationListOptions(workspaceId, projectId), enabled: !!projectId && kind === "service" });
  const create = useCreateApplication(workspaceId);
  const update = useUpdateApplication(workspaceId, initial?.id ?? "");
  const pending = create.isPending || update.isPending;
  const draftKey = JSON.stringify({ name, description, projectDraft, kind, config, program, argumentItems, environmentText, localEnvText, prepare });
  const [initialDraftKey] = useState(draftKey);
  const dirty = draftKey !== initialDraftKey;
  useNavigationGuard(useCallback(() => !pending && (!dirty || confirmNavigation({ message: `${t(($) => $.discard_title)}\n${t(($) => $.discard_help)}`, cancelLabel: t(($) => $.keep_editing), confirmLabel: t(($) => $.discard) })), [dirty, pending, t]), dirty || pending);
  useEffect(() => onStateChange({ dirty, pending }), [dirty, pending, onStateChange]);
  const set = <K extends keyof ApplicationConfig>(key: K, value: ApplicationConfig[K]) => setConfig((current) => ({ ...current, [key]: value }));
  const applyTemplate = (value: string) => {
    setTemplate(value);
    if (value === "custom") return;
    const templates = {
      node: { program: "npm", args: ["run", "dev", "--", "--host", "0.0.0.0"], port: 3000, path: "/" },
      python: { program: "python", args: ["-m", "http.server", "8000"], port: 8000, path: "/" },
      go: { program: "go", args: ["run", "."], port: 8080, path: "/health" },
    } as const;
    const selectedTemplate = templates[value as keyof typeof templates];
    if (!selectedTemplate) return;
    setProgram(selectedTemplate.program);
    setArgumentItems([...selectedTemplate.args]);
    setArgumentsText(JSON.stringify(selectedTemplate.args));
    set("port", selectedTemplate.port);
    set("health", { ...config.health, kind: "http", path: selectedTemplate.path });
  };
  const updateArguments = (items: string[]) => { setArgumentItems(items); setArgumentsText(JSON.stringify(items)); };
  const importConfiguration = () => {
    try {
      const imported = parseApplicationImport(importFormat, importText);
      if (imported.command.length > 0) {
        setProgram(imported.command[0] ?? "");
        updateArguments(imported.command.slice(1));
      }
      if (Object.keys(imported.environment).length > 0) setEnvironmentText(applicationEnvironmentText(imported.environment));
      if (imported.port > 0) set("port", imported.port);
      if (imported.entryPath) set("entry_path", imported.entryPath);
      setError("");
    } catch (cause) { setError(cause instanceof Error ? cause.message : t(($) => $.import_failed)); }
  };

  const save = async (event: FormEvent) => {
    event.preventDefault();
    if (!name.trim() || !projectId) { setError(t(($) => $.invalid_config)); return; }
    let next: ApplicationConfig;
    try {
      next = kind === "composition" ? defaultApplicationConfig() : {
        ...config, command: config.mode === "managed" ? [program.trim(), ...parseApplicationArguments(argumentsText)] : [],
        prepare: config.mode === "managed" ? prepare.map((step) => ({ args: [step.program.trim(), ...parseApplicationArguments(step.arguments)], timeout_seconds: step.timeout })) : [],
        environment: config.mode === "managed" ? parseApplicationEnvironment(environmentText) : {}, local_env: config.mode === "managed" ? parseApplicationEnvironment(localEnvText) : {},
        restart: config.mode === "managed" ? config.restart : { ...config.restart, enabled: false, restore: false },
        connections: config.mode === "managed" ? config.connections : [],
      };
      if (kind === "service" && (next.mode === "unknown" || next.mode === "managed" && (!program.trim() || !next.resource_id) || next.port < 0 || next.port > 65535 || next.mode === "external" && next.port === 0)) throw new Error();
    } catch { setError(t(($) => $.invalid_config)); return; }
    setError("");
    try {
      const application = baseline ? await update.mutateAsync({ revision: baseline.revision, name: name.trim(), description: description.trim(), config: next }) : await create.mutateAsync({ project_id: projectId, name: name.trim(), description: description.trim(), kind, config: next });
      toast.success(initial ? t(($) => $.saved) : t(($) => $.created));
      onSaved(application);
    } catch (cause) { setError(cause instanceof Error ? cause.message : t(($) => $.invalid_config)); }
  };

  return <>
    <form id={formId} onSubmit={save} className="min-h-0 flex-1 space-y-5 overflow-y-auto px-1 pb-2">
      <div className="grid gap-4 sm:grid-cols-2">
        <FormField label={t(($) => $.name)}><Input className="mt-1.5" value={name} onChange={(event) => setName(event.target.value)} maxLength={120} required autoFocus /></FormField>
        <FormField label={t(($) => $.project)}><ApplicationSelect value={projectId} onChange={(value) => { setProjectId(value); set("resource_id", ""); }} items={(projects.data ?? []).map((project) => ({ value: project.id, label: project.title }))} label={t(($) => $.project)} disabled={!!initial || projects.isPending} /></FormField>
        <FormField label={t(($) => $.kind)}><ApplicationSelect value={kind} onChange={(value) => setKind(value === "composition" ? "composition" : "service")} items={[{ value: "service", label: t(($) => $.service) }, { value: "composition", label: t(($) => $.composition) }]} label={t(($) => $.kind)} disabled={!!initial} /></FormField>
        {kind === "service" && <FormField label={t(($) => $.mode)}><ApplicationSelect value={config.mode} onChange={(value) => set("mode", value === "external" ? "external" : "managed")} items={[{ value: "managed", label: t(($) => $.managed) }, { value: "external", label: t(($) => $.external) }]} label={t(($) => $.mode)} /></FormField>}
      </div>
      <FormField label={t(($) => $.description)}><Textarea className="mt-1.5" value={description} onChange={(event) => setDescription(event.target.value)} maxLength={4000} /></FormField>
      {projects.isError && <p role="alert" className="text-caption text-destructive">{projects.error.message}</p>}
      {kind === "service" && <>
        {config.mode === "external" ? <p className="text-caption text-muted-foreground">{t(($) => $.external_help)}</p> : <>
          <FormField label={t(($) => $.resource)} help={!resources.isPending && !resources.data?.length ? t(($) => $.no_resources) : undefined}>
            <ApplicationSelect value={config.resource_id} onChange={(value) => set("resource_id", value)} items={(resources.data ?? []).map((resource) => ({ value: resource.id, label: resource.label || resource.resource_type }))} label={t(($) => $.select_resource)} disabled={!projectId || resources.isPending} />
          </FormField>
          {resources.isError && <p role="alert" className="text-caption text-destructive">{resources.error.message}</p>}
          {!resources.isPending && !resources.isError && projectId && !resources.data?.length && <AppLink href={paths.projectDetail(projectId)} className="text-body underline underline-offset-4">{t(($) => $.add_resources)}</AppLink>}
          <div className="grid gap-4 sm:grid-cols-2">
            <FormField label={t(($) => $.template)} help={t(($) => $.template_help)}><ApplicationSelect value={template} onChange={applyTemplate} items={[{ value: "custom", label: t(($) => $.template_custom) }, { value: "node", label: t(($) => $.template_node) }, { value: "python", label: t(($) => $.template_python) }, { value: "go", label: t(($) => $.template_go) }]} label={t(($) => $.template)} /></FormField>
            <FormField label={t(($) => $.program)}><Input className="mt-1.5 font-mono" value={program} onChange={(event) => setProgram(event.target.value)} required /></FormField>
            <FormField label={t(($) => $.arguments)} help={t(($) => $.arguments_help)}><div className="mt-1.5 space-y-2">{argumentItems.map((argument, index) => <div key={`${index}-${argument}`} className="flex items-center gap-2"><Input className="font-mono" value={argument} aria-label={`${t(($) => $.arguments)} ${index + 1}`} onChange={(event) => updateArguments(argumentItems.map((current, row) => row === index ? event.target.value : current))} /><Button type="button" variant="ghost" size="icon-sm" aria-label={t(($) => $.remove_argument)} onClick={() => updateArguments(argumentItems.filter((_current, row) => row !== index))}><Trash2 /></Button></div>)}<Button type="button" variant="outline" size="xs" onClick={() => updateArguments([...argumentItems, ""])}><Plus />{t(($) => $.add_argument)}</Button></div></FormField>
            <FormField label={t(($) => $.work_dir)}><Input className="mt-1.5 font-mono" value={config.work_dir} onChange={(event) => set("work_dir", event.target.value)} /></FormField>
            <FormField label={t(($) => $.ref)}><Input className="mt-1.5 font-mono" value={config.ref} onChange={(event) => set("ref", event.target.value)} /></FormField>
          </div>
        </>}
        <div className="grid gap-4 sm:grid-cols-2">
          <FormField label={t(($) => $.port)} help={t(($) => $.port_help)}><Input className="mt-1.5" type="number" min={0} max={65535} value={config.port} onChange={(event) => set("port", event.target.valueAsNumber)} /></FormField>
          <FormField label={t(($) => $.health)}><ApplicationSelect value={config.health.kind} onChange={(value) => set("health", { ...config.health, kind: value === "http" ? "http" : value === "none" ? "none" : "tcp", path: config.health.path || "/" })} items={[{ value: "tcp", label: t(($) => $.health_tcp) }, { value: "http", label: t(($) => $.health_http) }, { value: "none", label: t(($) => $.health_none) }]} label={t(($) => $.health)} /></FormField>
          {config.health.kind === "http" && <FormField label={t(($) => $.health_path)}><Input className="mt-1.5 font-mono" value={config.health.path} onChange={(event) => set("health", { ...config.health, path: event.target.value })} /></FormField>}
        </div>
        <details className="rounded-lg border p-3">
          <summary className="cursor-pointer text-body font-medium">{t(($) => $.advanced)}</summary>
          <div className="mt-4 space-y-4">
            {config.mode === "managed" && <>
              <FormField label={t(($) => $.environment)}><Textarea className="mt-1.5 min-h-20 font-mono" value={environmentText} onChange={(event) => setEnvironmentText(event.target.value)} /></FormField>
              <FormField label={t(($) => $.local_env)} help={t(($) => $.local_env_help)}><Textarea className="mt-1.5 min-h-20 font-mono" value={localEnvText} onChange={(event) => setLocalEnvText(event.target.value)} /></FormField>
              <section className="space-y-3">
                <div className="flex items-center justify-between gap-3"><h3 className="text-body font-medium">{t(($) => $.connections)}</h3><Button type="button" variant="outline" size="xs" disabled={config.connections.length >= 16} onClick={() => set("connections", [...config.connections, { target_id: "", url_variable: "" }])}><Plus />{t(($) => $.add_connection)}</Button></div>
                <p className="text-caption text-muted-foreground">{t(($) => $.connections_help)}</p>
                {config.connections.map((binding, index) => <div key={index} className="grid gap-3 rounded-lg border p-3 sm:grid-cols-[1fr_1fr_auto]">
                  <FormField label={t(($) => $.connection_target)}><ApplicationSelect value={binding.target_id} onChange={(value) => set("connections", config.connections.map((current, position) => position === index ? { ...current, target_id: value } : current))} label={t(($) => $.connection_target)} items={(applications.data?.applications ?? []).filter((app) => app.kind === "service" && app.id !== initial?.id).map((app) => ({ value: app.id, label: app.name }))} /></FormField>
                  <FormField label={t(($) => $.connection_variable)}><Input value={binding.url_variable} onChange={(event) => set("connections", config.connections.map((current, position) => position === index ? { ...current, url_variable: event.target.value } : current))} /></FormField>
                  <Button type="button" variant="ghost" size="icon-sm" aria-label={t(($) => $.remove_connection)} onClick={() => set("connections", config.connections.filter((_, position) => position !== index))}><Trash2 /></Button>
                </div>)}
              </section>
              <div className="space-y-3"><div className="flex items-center justify-between gap-3"><span className="text-caption font-medium">{t(($) => $.prepare)}</span><Button type="button" size="xs" variant="outline" onClick={() => setPrepare((current) => [...current, { program: "", arguments: "[]", timeout: 300 }])}><Plus />{t(($) => $.add_prepare)}</Button></div>
                {prepare.map((step, index) => <div key={index} className="grid grid-cols-[1fr_auto] gap-2 rounded-md border p-3">
                  <Input aria-label={`${t(($) => $.program)} ${index + 1}`} value={step.program} onChange={(event) => setPrepare((current) => current.map((entry, row) => row === index ? { ...entry, program: event.target.value } : entry))} />
                  <Button type="button" size="icon-sm" variant="ghost" aria-label={t(($) => $.remove)} onClick={() => setPrepare((current) => current.filter((_entry, row) => row !== index))}><Trash2 /></Button>
                  <Input aria-label={`${t(($) => $.arguments)} ${index + 1}`} value={step.arguments} onChange={(event) => setPrepare((current) => current.map((entry, row) => row === index ? { ...entry, arguments: event.target.value } : entry))} />
                  <Input aria-label={t(($) => $.timeout)} className="w-24" type="number" min={1} max={1800} value={step.timeout} onChange={(event) => setPrepare((current) => current.map((entry, row) => row === index ? { ...entry, timeout: event.target.valueAsNumber } : entry))} />
                </div>)}
              </div>
              <label className="flex items-center justify-between gap-4 text-body">{t(($) => $.restart_policy)}<Switch checked={config.restart.enabled} onCheckedChange={(enabled) => set("restart", { ...config.restart, enabled })} /></label>
              {config.restart.enabled && <div className="grid gap-4 sm:grid-cols-2"><FormField label={t(($) => $.restart_attempts)}><Input className="mt-1.5" type="number" min={1} max={10} value={config.restart.max_attempts} onChange={(event) => set("restart", { ...config.restart, max_attempts: event.target.valueAsNumber })} /></FormField><FormField label={t(($) => $.restart_delay)}><Input className="mt-1.5" type="number" min={1} max={300} value={config.restart.delay_seconds} onChange={(event) => set("restart", { ...config.restart, delay_seconds: event.target.valueAsNumber })} /></FormField></div>}
              <label className="flex items-center justify-between gap-4 text-body">{t(($) => $.restore)}<Switch checked={config.restart.restore} onCheckedChange={(restore) => set("restart", { ...config.restart, restore })} /></label>
            </>}
            <div className="grid gap-4 sm:grid-cols-2"><FormField label={t(($) => $.timeout)}><Input className="mt-1.5" type="number" min={1} max={600} value={config.health.timeout_seconds} onChange={(event) => set("health", { ...config.health, timeout_seconds: event.target.valueAsNumber })} /></FormField><FormField label={t(($) => $.interval)}><Input className="mt-1.5" type="number" min={1} max={60} value={config.health.interval_seconds} onChange={(event) => set("health", { ...config.health, interval_seconds: event.target.valueAsNumber })} /></FormField></div>
            <FormField label={t(($) => $.entry_path)}><Input className="mt-1.5 font-mono" value={config.entry_path} onChange={(event) => set("entry_path", event.target.value)} /></FormField>
            <label className="flex items-center justify-between gap-4 text-body">{t(($) => $.auto_publish)}<Switch checked={config.auto_publish} disabled={config.port === 0} onCheckedChange={(value) => set("auto_publish", value)} /></label>
          </div>
          <section className="space-y-3 rounded-lg border p-3"><div><h3 className="text-body font-medium">{t(($) => $.import_config)}</h3><p className="text-caption text-muted-foreground">{t(($) => $.import_config_help)}</p></div><div className="grid gap-3 sm:grid-cols-[12rem_1fr]"><ApplicationSelect value={importFormat} onChange={(value) => setImportFormat(value as ApplicationImportFormat)} items={[{ value: "compose", label: t(($) => $.import_compose) }, { value: "procfile", label: t(($) => $.import_procfile) }, { value: "package", label: t(($) => $.import_package) }]} label={t(($) => $.import_format)} /><Textarea value={importText} onChange={(event) => setImportText(event.target.value)} placeholder={t(($) => $.import_placeholder)} className="min-h-24 font-mono text-caption" /></div><Button type="button" variant="outline" size="sm" onClick={importConfiguration} disabled={!importText.trim()}>{t(($) => $.import_apply)}</Button></section>
        </details>
      </>}
      {error && <p role="alert" className="break-words text-caption text-destructive">{error}</p>}
    </form>
    <DialogFooter className="shrink-0 border-t pt-4"><DialogClose render={<Button variant="outline" disabled={pending} />}>{t(($) => $.cancel)}</DialogClose><Button type="submit" form={formId} disabled={pending} aria-busy={pending}>{pending && <Loader2 className="animate-spin" />}{t(($) => $.save)}</Button></DialogFooter>
  </>;
}

export function ApplicationEditor({ workspaceId, application, projectId, trigger, onSaved }: { workspaceId: string; application?: Application; projectId?: string; trigger: ReactElement; onSaved?: (application: Application) => void }) {
  const { t } = useT("applications");
  const [open, setOpen] = useState(false);
  const [state, setState] = useState<EditorState>({ dirty: false, pending: false });
  const [discardOpen, setDiscardOpen] = useState(false);
  const changeOpen = (next: boolean) => {
    if (next) { setState({ dirty: false, pending: false }); setOpen(true); return; }
    if (state.pending) return;
    if (state.dirty) { setDiscardOpen(true); return; }
    setOpen(false);
  };
  return <><Dialog open={open} onOpenChange={changeOpen}>
    <DialogTrigger render={trigger} />
    <DialogContent className="flex max-h-[90%] flex-col sm:max-w-2xl" aria-describedby={undefined}>
      <DialogHeader><DialogTitle>{application ? t(($) => $.edit) : t(($) => $.new)}</DialogTitle></DialogHeader>
      {open && <EditorForm workspaceId={workspaceId} initial={application} projectId={projectId} onStateChange={setState} onSaved={(saved) => runGuardedNavigation(() => { setOpen(false); onSaved?.(saved); }, true)} />}
    </DialogContent>
  </Dialog>
    <AlertDialog open={discardOpen} onOpenChange={setDiscardOpen}>
      <AlertDialogContent>
        <AlertDialogHeader><AlertDialogTitle>{t(($) => $.discard_title)}</AlertDialogTitle><AlertDialogDescription>{t(($) => $.discard_help)}</AlertDialogDescription></AlertDialogHeader>
        <AlertDialogFooter><AlertDialogCancel>{t(($) => $.keep_editing)}</AlertDialogCancel><AlertDialogAction variant="destructive" onClick={() => { setDiscardOpen(false); setOpen(false); }}>{t(($) => $.discard)}</AlertDialogAction></AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </>;
}
