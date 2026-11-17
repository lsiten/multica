"use client";

import { useQuery } from "@tanstack/react-query";
import { useWorkspaceFileOperationStore, workspaceFileOperationScope, type WorkspaceFileOperationContext } from "@multica/core/chat";
import { workspaceFileOperationCapabilitiesOptions } from "@multica/core/chat/queries";
import { Ellipsis, FolderInput, Trash2 } from "@multica/ui/icons";
import { IconButton } from "@multica/ui/components/common/icon-button";
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem } from "@multica/ui/components/ui/dropdown-menu";
import { useT } from "../../i18n";
import type { WorkspaceFileChoice } from "./workspace-file-operation-dialog";

export function WorkspaceFileActions({ context, path, fileId, directory, supported, onChoose }: {
  readonly context: WorkspaceFileOperationContext;
  readonly path: string;
  readonly fileId: string;
  readonly directory: boolean;
  readonly supported: boolean;
  readonly onChoose: (choice: WorkspaceFileChoice) => void;
}) {
  const { t } = useT("chat");
  const { data } = useQuery(workspaceFileOperationCapabilitiesOptions());
  const pending = useWorkspaceFileOperationStore((state) => !!state.pending[workspaceFileOperationScope(context)]);
  if (!supported || data?.workspace_file_selection_supported !== true || data.workspace_file_operations_supported !== true) return null;
  return <DropdownMenu><DropdownMenuTrigger render={<IconButton size="icon-xs" className="shrink-0" label={`${t(($) => $.file_operations.actions)} ${path}`} disabled={pending} />}><Ellipsis /></DropdownMenuTrigger>
    <DropdownMenuContent align="end">
      <DropdownMenuItem onClick={() => onChoose({ context, directory, operation: { kind: "move", path, destination: path, expected_file_id: fileId } })}><FolderInput />{t(($) => $.file_operations.move)}</DropdownMenuItem>
      <DropdownMenuItem variant="destructive" onClick={() => onChoose({ context, directory, operation: { kind: "trash", path, expected_file_id: fileId } })}><Trash2 />{t(($) => $.file_operations.trash)}</DropdownMenuItem>
    </DropdownMenuContent>
  </DropdownMenu>;
}
