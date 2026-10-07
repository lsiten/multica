"use client";

import { useRef, useState, type ReactElement } from "react";
import { useQuery } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";
import { applicationPlanOptions, useApplicationOperation, type Application, type ApplicationBoard, type ApplicationOperationRequest } from "@multica/core/applications";
import { useAuthStore } from "@multica/core/auth";
import { runtimeListOptions, isRuntimeUsableForUser } from "@multica/core/runtimes";
import { memberListOptions } from "@multica/core/workspace/queries";
import { Button } from "@multica/ui/components/ui/button";
import { Switch } from "@multica/ui/components/ui/switch";
import { Dialog, DialogClose, DialogContent, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@multica/ui/components/ui/dialog";
import { RuntimePicker } from "../agents/components/runtime-picker";
import { useT } from "../i18n";

type Action = ApplicationOperationRequest["action"];

export function useApplicationActionLabels() {
  const { t } = useT("applications");
  return { start: t(($) => $.start), stop: t(($) => $.stop), restart: t(($) => $.restart), publish: t(($) => $.publish), unpublish: t(($) => $.unpublish) };
}

function OperationForm({ workspaceId, application, board, action, initialRuntimeId, onAccepted }: { workspaceId: string; application: Application; board: ApplicationBoard; action: Action; initialRuntimeId?: string; onAccepted: () => void }) {
  const { t } = useT("applications");
  const labels = useApplicationActionLabels();
  const [runtimeId, setRuntimeId] = useState(initialRuntimeId ?? "");
  const [placements, setPlacements] = useState<Record<string, string>>({});
  const [force, setForce] = useState(false);
  const [error, setError] = useState("");
  const requestKey = useRef<{ signature: string; key: string } | null>(null);
  const userId = useAuthStore((state) => state.user?.id ?? null);
  const runtimes = useQuery(runtimeListOptions(workspaceId));
  const members = useQuery(memberListOptions(workspaceId));
  const plan = useQuery({ ...applicationPlanOptions(workspaceId, application.id, application.revision), enabled: action !== "stop" && action !== "unpublish" });
  const operation = useApplicationOperation(workspaceId, application.id);
  const capable = (runtimes.data ?? []).filter((runtime) => Array.isArray(runtime.metadata.capabilities) && runtime.metadata.capabilities.includes("applications-v1"));
  const selected = capable.find((runtime) => runtime.id === runtimeId);
  const usable = !!selected && isRuntimeUsableForUser(selected, userId);
  const byId = new Map(board.applications.map((app) => [app.id, app]));
  const placementIds = [...new Set((plan.data?.nodes ?? []).flatMap((node) => [node.id, ...node.dependencies.map((dependency) => dependency.id)]))];

  const submit = async () => {
    if (!usable) return;
    const input = { action, revision: application.revision, runtime_id: runtimeId, placements, force };
    const signature = JSON.stringify(input);
    if (requestKey.current?.signature !== signature) requestKey.current = { signature, key: crypto.randomUUID() };
    try {
      setError("");
      await operation.mutateAsync({ ...input, idempotency_key: requestKey.current.key });
      toast.success(t(($) => $.accepted));
      onAccepted();
    } catch (cause) { setError(cause instanceof Error ? cause.message : t(($) => $.invalid_config)); }
  };

  return <>
    <div className="min-h-0 space-y-4 overflow-y-auto py-1">
      <RuntimePicker runtimes={capable} runtimesLoading={runtimes.isPending} members={members.data ?? []} currentUserId={userId} selectedRuntimeId={runtimeId} onSelect={setRuntimeId} disabled={operation.isPending} />
      {!runtimes.isPending && !capable.length && <p role="alert" className="text-caption text-warning">{t(($) => $.runtime_upgrade)}</p>}
      {application.kind === "composition" && placementIds.length > 0 && <details className="rounded-lg border p-3"><summary className="cursor-pointer text-body font-medium">{t(($) => $.placement)}</summary><div className="mt-4 space-y-4">{placementIds.map((id) => <div key={id} className="space-y-2"><p className="break-words text-caption font-medium">{byId.get(id)?.name ?? id}</p><RuntimePicker runtimes={capable} members={members.data ?? []} currentUserId={userId} selectedRuntimeId={placements[id] ?? runtimeId} onSelect={(value) => setPlacements((current) => ({ ...current, [id]: value }))} disabled={operation.isPending} /></div>)}</div></details>}
      {(action === "stop" || action === "restart") && <div className="space-y-3 rounded-lg border p-3"><p className="text-caption text-muted-foreground">{t(($) => $.force_help)}</p><label className="flex items-center justify-between gap-4 text-caption">{t(($) => $.force)}<Switch checked={force} onCheckedChange={setForce} disabled={operation.isPending} /></label></div>}
      {plan.isError && action !== "stop" && action !== "unpublish" && <p role="alert" className="break-words text-caption text-destructive">{plan.error.message}</p>}
      {error && <p role="alert" className="break-words text-caption text-destructive">{error}</p>}
    </div>
    <DialogFooter><DialogClose render={<Button variant="outline" disabled={operation.isPending} />}>{t(($) => $.cancel)}</DialogClose><Button disabled={!usable || operation.isPending || plan.isError && action !== "stop" && action !== "unpublish"} aria-busy={operation.isPending} onClick={() => void submit()}>{operation.isPending && <Loader2 className="animate-spin" />}{labels[action]}</Button></DialogFooter>
  </>;
}

export function ApplicationOperationDialog({ workspaceId, application, board, action, initialRuntimeId, trigger }: { workspaceId: string; application: Application; board: ApplicationBoard; action: Action; initialRuntimeId?: string; trigger: ReactElement }) {
  const labels = useApplicationActionLabels();
  const [open, setOpen] = useState(false);
  return <Dialog open={open} onOpenChange={setOpen}><DialogTrigger render={trigger} /><DialogContent className="flex max-h-[90dvh] flex-col sm:max-w-lg" aria-describedby={undefined}><DialogHeader><DialogTitle>{labels[action]} · {application.name}</DialogTitle></DialogHeader>{open && <OperationForm workspaceId={workspaceId} application={application} board={board} action={action} initialRuntimeId={initialRuntimeId} onAccepted={() => setOpen(false)} />}</DialogContent></Dialog>;
}
