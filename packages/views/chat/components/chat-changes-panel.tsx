"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import type { PagedReviewInput, ReviewManifest } from "@multica/core/types/local-review-pages";
import { Loader2 } from "@multica/ui/icons";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";
import { LocalReviewIndex } from "../../issues/components/local-review-index";
import { readReviewManifest, readReviewVersions } from "../../platform/local-review-pages";
import { LocalReviewError } from "../../issues/components/local-review-error";

type ReviewRequest = Omit<PagedReviewInput, "action">;

export function ChatChangesPanel({
  request,
  onChanged,
  onInvalidBinding,
}: {
  readonly request: ReviewRequest;
  readonly onChanged: () => void;
  readonly onInvalidBinding: () => void;
}) {
  const { t } = useT("chat");
  const [mode, setMode] = useState<"current" | "version">("current");
  const [versionId, setVersionId] = useState("");
  const versions = useQuery({
    queryKey: ["chat-review-versions", request.workspace_id, request.task_id, request.project_resource_id, request.runtime_id, request.path, request.target],
    queryFn: ({ signal }) => readReviewVersions({ ...request, action: "versions" }, signal),
    staleTime: 15_000,
    retry: false,
    refetchOnWindowFocus: false,
    networkMode: "always",
  });
  const selected = useMemo(
    () => versions.data?.versions.find((version) => version.version_id === versionId) ?? versions.data?.versions[0],
    [versionId, versions.data?.versions],
  );
  useEffect(() => {
    if (!versionId && versions.data?.versions[0]) setVersionId(versions.data.versions[0].version_id);
  }, [versionId, versions.data?.versions]);

  return (
    <div className="space-y-2">
      <div className="flex gap-1 rounded-md border bg-muted/20 p-1" role="tablist" aria-label={t(($) => $.doc_file_list.changes_scope_label)}>
        <Button type="button" size="xs" variant={mode === "current" ? "secondary" : "ghost"} role="tab" aria-selected={mode === "current"} onClick={() => setMode("current")}>
          {request.task_id ? t(($) => $.doc_file_list.changes_scope_run) : t(($) => $.doc_file_list.changes_scope_directory)}
        </Button>
        <Button type="button" size="xs" variant={mode === "version" ? "secondary" : "ghost"} role="tab" aria-selected={mode === "version"} onClick={() => setMode("version")}>
          {t(($) => $.doc_file_list.changes_scope_version)}
        </Button>
      </div>
      {mode === "current" ? (
        <LocalReviewIndex
          request={request}
          onChanged={onChanged}
          onBusyChange={() => undefined}
          onInvalidBinding={onInvalidBinding}
        />
      ) : (
        <section className="space-y-2">
          {versions.isPending && <p role="status" className="flex items-center gap-2 p-2 text-caption text-muted-foreground"><Loader2 className="size-4 animate-spin" />{t(($) => $.doc_file_list.changes_version_loading)}</p>}
          {versions.error && <LocalReviewError error={versions.error} id="chat-review-versions-error" />}
          {!versions.isPending && !versions.error && versions.data?.versions.length === 0 && <p className="p-2 text-caption text-muted-foreground">{t(($) => $.doc_file_list.changes_versions_empty)}</p>}
          {versions.data?.versions.length ? (
            <label className="flex items-center gap-2 text-caption">
              <span className="shrink-0 text-muted-foreground">{t(($) => $.doc_file_list.changes_scope_version)}</span>
              <select className="min-w-0 flex-1 rounded-md border bg-background px-2 py-1 text-caption" value={selected?.version_id ?? ""} onChange={(event) => setVersionId(event.target.value)} aria-label={t(($) => $.doc_file_list.changes_scope_version)}>
                {versions.data.versions.map((version) => <option key={version.version_id} value={version.version_id}>{version.header.branch} · {version.version_id.slice(0, 8)} · {new Date(version.updated_at).toLocaleString()}</option>)}
              </select>
            </label>
          ) : null}
          {selected && <ChatReviewVersionSnapshot request={request} versionId={selected.version_id} target={selected.header.target} />}
        </section>
      )}
    </div>
  );
}

function ChatReviewVersionSnapshot({ request, versionId, target }: { readonly request: ReviewRequest; readonly versionId: string; readonly target: string }) {
  const { t } = useT("chat");
  const query = useQuery<ReviewManifest>({
    queryKey: ["chat-review-version", request.workspace_id, request.task_id, request.project_resource_id, request.path, target, versionId],
    queryFn: ({ signal }) => readReviewManifest({ ...request, scope: "version", target, action: "manifest", version_id: versionId }, signal),
    staleTime: 30_000,
    retry: false,
    refetchOnWindowFocus: false,
    networkMode: "always",
  });
  if (query.isPending) return <p role="status" className="flex items-center gap-2 p-2 text-caption text-muted-foreground"><Loader2 className="size-4 animate-spin" />{t(($) => $.doc_file_list.changes_version_loading)}</p>;
  if (query.error) return <LocalReviewError error={query.error} id="chat-review-version-error" />;
  const data = query.data;
  if (!data) return null;
  return <div className="space-y-2 rounded-md border p-2"><p className="text-caption text-muted-foreground">{t(($) => $.doc_file_list.changes_version_summary, { count: data.page.total_files, additions: data.page.additions, deletions: data.page.deletions })}</p><ul className="divide-y rounded-md border">{data.page.files.map((file) => <li key={`${file.path}:${file.status}`} className="flex items-center gap-2 px-2 py-1.5 text-caption"><span className="min-w-0 flex-1 truncate font-mono">{file.path}</span><span className="shrink-0 text-muted-foreground">{file.status}</span></li>)}</ul></div>;
}
