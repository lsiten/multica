"use client";

import { useState } from "react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import type { WorkspaceFileOperationContext } from "@multica/core/chat";
import { workspaceFileOperationCapabilitiesOptions } from "@multica/core/chat/queries";
import { Button } from "@multica/ui/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogTrigger } from "@multica/ui/components/ui/dialog";
import { File, Folder, Loader2, Trash2 } from "@multica/ui/icons";
import { requestReviewPage } from "../../platform/local-review-pages";
import { useT } from "../../i18n";
import type { WorkspaceFileChoice } from "./workspace-file-operation-dialog";

export function WorkspaceFileTrash({ context, supported, onChoose }: { readonly context: WorkspaceFileOperationContext; readonly supported: boolean; readonly onChoose: (choice: WorkspaceFileChoice) => void }) {
  const { t } = useT("chat");
  const [open, setOpen] = useState(false);
  const capability = useQuery(workspaceFileOperationCapabilitiesOptions());
  const query = useInfiniteQuery({
    queryKey: ["workspace-file-trash", context.workspace_id, context.runtime_id, context.task_id, context.path],
    initialPageParam: 0,
    queryFn: async ({ pageParam, signal }) => {
      const result = await requestReviewPage({ ...context, action: "workspace_trash", offset: pageParam, limit: 50 }, signal);
      if (!("kind" in result) || result.kind !== "workspace_trash") throw new Error("Trash page expected");
      return result;
    },
    getNextPageParam: (page) => page.has_more ? page.next_offset : undefined,
    enabled: open, retry: false,
  });
  if (!supported || capability.data?.workspace_file_selection_supported !== true || capability.data.workspace_file_operations_supported !== true) return null;
  const items = query.data?.pages.flatMap((page) => page.items) ?? [];
  return <Dialog open={open} onOpenChange={setOpen}>
    <DialogTrigger render={<Button size="sm" variant="ghost" />}><Trash2 />{t(($) => $.file_operations.recycle_bin)}</DialogTrigger>
    <DialogContent>
      <DialogHeader><DialogTitle>{t(($) => $.file_operations.recycle_bin)}</DialogTitle></DialogHeader>
      <p className="break-all font-mono text-caption text-muted-foreground" dir="ltr">{context.path}</p>
      <div className="max-h-[min(30rem,65dvh)] space-y-2 overflow-y-auto">
        {query.isPending && <p role="status" className="flex items-center gap-2 text-body"><Loader2 className="size-4 animate-spin motion-reduce:animate-none" />{t(($) => $.doc_file_list.loading)}</p>}
        {!query.isPending && !query.isError && !items.length && <p className="text-body text-muted-foreground">{t(($) => $.file_operations.empty)}</p>}
        {items.map((item) => {
          const Icon = item.directory ? Folder : File;
          return <div key={item.recovery_id} className="flex min-w-0 items-center gap-2 rounded-md p-2 hover:bg-accent/50">
            <Icon className="size-4 shrink-0 text-muted-foreground" />
            <div className="min-w-0 flex-1"><p className="break-all text-body" dir="ltr">{item.path}</p>{item.deleted_at && <time dateTime={item.deleted_at} className="text-caption text-muted-foreground">{new Date(item.deleted_at).toLocaleString()}</time>}</div>
            <Button size="sm" variant="outline" onClick={() => { setOpen(false); onChoose({ context, directory: item.directory, operation: { kind: "restore", path: item.path, recovery_id: item.recovery_id } }); }}>{t(($) => $.file_operations.restore)}</Button>
          </div>;
        })}
        {query.isError && <div role="alert" className="space-y-2 text-body text-destructive"><p>{t(($) => $.doc_file_list.load_failed)}</p><Button size="sm" variant="outline" onClick={() => void query.refetch()}>{t(($) => $.doc_file_list.retry)}</Button></div>}
        {query.hasNextPage && <Button variant="ghost" size="sm" disabled={query.isFetching} onClick={() => void query.fetchNextPage()}>{t(($) => $.doc_file_list.load_more)}</Button>}
      </div>
    </DialogContent>
  </Dialog>;
}
