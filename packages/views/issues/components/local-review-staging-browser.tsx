import { useEffect, useId, useMemo, useState } from "react";
import { useIsMutating, useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import type { LocalReviewRequest } from "@multica/core/types/local-review";
import type { ReviewManifest } from "@multica/core/types/local-review-pages";
import { changeLocalIndex, readLocalIndex, readReviewManifest, renewReviewLease } from "../../platform/local-review-pages";
import { LocalReviewFileBrowser } from "./local-review-file-browser";
import { LocalReviewError } from "./local-review-error";
import { LocalReviewSelection } from "./local-review-selection";
import { LocalReviewIndex } from "./local-review-index";
import { readAllReviewPaths } from "../../platform/local-review-all-files";

export function LocalReviewStagingBrowser({ request, manifest, disabled: externallyDisabled, onBusyChange, onChanged, onSelectionBusy = onBusyChange, onSelectedMerged }: {
  request: LocalReviewRequest; manifest: ReviewManifest; disabled: boolean; onBusyChange: (busy: boolean) => void; onChanged: () => void; onSelectionBusy?: (busy: boolean) => void; onSelectedMerged?: (commit: string) => void;
}) {
  const userId = useAuthStore((state) => state.user?.id);
  const client = useQueryClient();
  const errorId = useId();
  const [pendingPaths, setPendingPaths] = useState<string[]>([]);
  const [selectingAll, setSelectingAll] = useState(false);
  const key = useMemo(() => ["local-index", userId, request.workspace_id, request.task_id, request.path], [userId, request.workspace_id, request.task_id, request.path]);
  const pending = useIsMutating({ mutationKey: key }) > 0;
  const allFiles = useQuery({ queryKey: ["all-review-paths", userId, request.workspace_id, request.task_id, manifest.version_id], queryFn: ({ signal }) => readAllReviewPaths({ request, manifest }, signal), enabled: false, networkMode: "always", retry: false, staleTime: Infinity });
  const query = useQuery({ queryKey: key, queryFn: ({ signal }) => readLocalIndex({ ...request, action: "index" }, signal), staleTime: 30000, networkMode: "always", retry: false, refetchOnWindowFocus: false });
  const leases = useQueries({ queries: [query.data?.version_id, query.data?.staged_version_id, query.data?.unstaged_version_id].filter((id): id is string => !!id).map((versionId) => ({
    queryKey: ["local-index-lease", userId, request.workspace_id, request.task_id, request.path, versionId],
    queryFn: ({ signal }: { signal: AbortSignal }) => renewReviewLease({ ...request, action: "lease", target: query.data?.status.branch, runtime_id: request.runtime_id || query.data?.runtime_id, version_id: versionId }, signal),
    retry: false, networkMode: "always" as const, refetchInterval: 60000, refetchIntervalInBackground: true,
  })) });
  const staged = useQuery({
    queryKey: ["index-staged-diff", userId, request.workspace_id, request.task_id, request.path, query.data?.staged_version_id],
    queryFn: ({ signal }) => readReviewManifest({ ...request, action: "files", target: query.data?.status.branch, runtime_id: request.runtime_id || query.data?.runtime_id, version_id: query.data?.staged_version_id }, signal),
    enabled: !!query.data?.staged_version_id, staleTime: Infinity, networkMode: "always", retry: false, refetchOnWindowFocus: false,
  });
  const mutation = useMutation({
    // Mutations still use the HEAD-based identity, not either preview identity.
    mutationKey: key, networkMode: "always",
    mutationFn: ({ action, path }: { action: "stage" | "unstage"; path: string }) => {
      const data = query.data;
      if (!data) throw new Error("Load Git index before modifying it");
      return changeLocalIndex({ ...request, action, target: data.status.branch, branch: data.status.branch, head: data.status.head, index_id: data.status.index_id, version_id: data.version_id, snapshot_id: data.version_id, runtime_id: request.runtime_id || data.runtime_id, paths: [path] });
    },
    onSuccess: async () => { await client.invalidateQueries({ queryKey: key, exact: true }); onChanged(); },
  });
  const unstaged = useQuery({
    queryKey: ["index-unstaged-diff", userId, request.workspace_id, request.task_id, request.path, query.data?.unstaged_version_id],
    queryFn: ({ signal }) => readReviewManifest({ ...request, action: "files", target: query.data?.status.branch, runtime_id: request.runtime_id || query.data?.runtime_id, version_id: query.data?.unstaged_version_id }, signal),
    enabled: !!query.data?.unstaged_version_id, staleTime: Infinity, networkMode: "always", retry: false, refetchOnWindowFocus: false,
  });
  const previewError = staged.error ?? (query.data && query.data.status.files.some((file) => file.staged) && !query.data.staged_version_id ? new Error("local_review_index_upgrade_required") : undefined);
  const workingPreviewError = unstaged.error ?? (query.data && query.data.status.files.some((file) => file.unstaged) && !query.data.unstaged_version_id ? new Error("local_review_index_upgrade_required") : undefined);
  const addAll = async () => {
    setSelectingAll(true);
    try {
      const index = await query.refetch();
      if (index.error || !index.data) return;
      const result = await allFiles.refetch();
      if (result.error || !result.data) return;
      const working = new Set(index.data.status.files.map((file) => file.path));
      setPendingPaths((paths) => [...new Set([...paths, ...result.data.filter((path) => !working.has(path))])]);
    } finally {
      setSelectingAll(false);
    }
  };
  const disabled = externallyDisabled || selectingAll;
  const actionError = mutation.error ?? allFiles.error;
  useEffect(() => { onBusyChange(pending); return () => onBusyChange(client.isMutating({ mutationKey: key }) > 0); }, [pending, onBusyChange, client, key]);
  return <>
    {actionError && <LocalReviewError error={actionError} id={errorId} />}
    <LocalReviewFileBrowser request={request} manifest={manifest} selection={{ files: pendingPaths.map((path) => ({ path })), busy: disabled || pending || allFiles.isFetching, addAll: () => { void addAll(); }, removeAll: () => setPendingPaths([]), add: (file) => setPendingPaths((paths) => paths.includes(file.path) ? paths : [...paths, file.path]), remove: (path) => setPendingPaths((paths) => paths.filter((entry) => entry !== path)) }} staging={{ files: query.data?.status.files ?? [], preview: staged.data, previewError, workingPreview: unstaged.data, workingPreviewError, busy: disabled || pending || query.isFetching, reason: (query.error ?? leases.find((lease) => lease.error)?.error)?.message, change: (action, path) => mutation.mutate({ action, path }) }} />
    {(!!pendingPaths.length || !!query.data?.status.files.length || !!query.error) && <section className="max-h-72 space-y-4 overflow-auto rounded border p-3">
      {!!pendingPaths.length && <LocalReviewSelection embedded request={request} manifest={manifest} paths={pendingPaths} disabled={disabled || pending || allFiles.isFetching} onBusyChange={onSelectionBusy} onMerged={(commit) => { setPendingPaths([]); if (onSelectedMerged) onSelectedMerged(commit); else onChanged(); }} />}
      {(!!query.data?.status.files.length || !!query.error) && <LocalReviewIndex compact request={request} disabled={disabled || allFiles.isFetching} onBusyChange={onBusyChange} onChanged={onChanged} />}
    </section>}
  </>;
}
