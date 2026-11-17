"use client";

import { useWorkspaceFileOperationStore, type PendingWorkspaceFileOperation } from "@multica/core/chat";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";

export function WorkspaceFilePending({ wsId, onResume }: { readonly wsId: string; readonly onResume: (pending: PendingWorkspaceFileOperation) => void }) {
  const { t } = useT("chat");
  const pending = useWorkspaceFileOperationStore((state) => state.pending);
  const commands = Object.values(pending).filter((command) => command.context.workspace_id === wsId);
  if (!commands.length) return null;
  return <div className="shrink-0 space-y-2 p-3" aria-label={t(($) => $.file_operations.pending)}>
    {commands.map((command) => <div key={command.commandId} className="flex min-w-0 items-center gap-2 text-caption">
      <span className="min-w-0 flex-1"><span className="block text-muted-foreground">{t(($) => $.file_operations.pending)}</span><span className="block truncate" title={`${command.context.path}/${command.operation.path}`}>{command.operation.path}</span></span>
      <Button size="sm" variant="outline" onClick={() => onResume(command)}>{t(($) => $.file_operations.resume)}</Button>
    </div>)}
  </div>;
}
