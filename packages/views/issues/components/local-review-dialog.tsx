"use client";

import { useEffect, useId, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { createPortal } from "react-dom";
import { skipToken, useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import { defaultStorage } from "@multica/core/platform";
import { createSafeId } from "@multica/core/utils";
import type { LocalReviewRequest } from "@multica/core/types/local-review";
import type { ReviewManifest } from "@multica/core/types/local-review-pages";
import { defaultReviewTarget } from "@multica/core/types/local-review-target";
import { Button } from "@multica/ui/components/ui/button";
import { Textarea } from "@multica/ui/components/ui/textarea";
import {
  ArrowLeft,
  Columns2,
  ExternalLink,
  FileCode,
  GitBranch,
  History,
  Loader2,
  MessageSquareText,
  RefreshCw,
  X,
} from "@multica/ui/icons";
import { readLocalReviewBranches } from "../../platform/local-review";
import { applyReviewFileEdit, readReviewCommits, readReviewContent, readReviewManifest, readReviewRepositories, renewReviewLease } from "../../platform/local-review-pages";
import { useLocale, useT } from "../../i18n";
import { LocalReviewError, reviewErrorKind } from "./local-review-error";
import { LocalReviewTargetPicker } from "./local-review-target-picker";
import { LocalReviewStagingBrowser } from "./local-review-staging-browser";
import { invalidateWorktreeInventory } from "../../platform/local-review-inventory";
import { localReviewActions, type LocalReviewDecision } from "./local-review-actions";
import { LocalReviewIndex } from "./local-review-index";
import { useOptionalNavigation } from "../../navigation";
import {
  parseReviewDiffLayout,
  parseReviewFileFilter,
  parseReviewFileView,
  type ReviewDiffLayout,
  type ReviewFileFilter,
  type ReviewFileView,
} from "./local-review-workspace-state";
import { canOpenLocalPath, openLocalPath } from "../../platform";
import type {
  LocalReviewComment,
  ReviewCommentSide,
  ReviewFindingKind,
} from "./local-review-comments";
import { parseLocalReviewComments } from "./local-review-comments";
import {
  appendLocalReviewReply,
  applyReviewFindingSuggestion,
} from "./local-review-comments";

export function LocalReviewDialog({ request, onClose }: { request: LocalReviewRequest; onClose: () => void }) {
  const { t } = useT("issues");
  const locale = useLocale();
  const navigation = useOptionalNavigation();
  const errorId = useId();
  const requestChangesHintId = useId();
  const dialogRef = useRef<HTMLDivElement>(null);
  const closeRef = useRef<HTMLButtonElement>(null);
  const loadedCommentsKey = useRef<string | null>(null);
  const loadedDismissedKey = useRef<string | null>(null);
  const layoutExplicit = useRef(
    navigation?.searchParams.has("review_layout") ?? false,
  );
  const client = useQueryClient();
  const userId = useAuthStore((state) => state.user?.id);
  const runtimeKey = ["local-review-runtime", userId, request.workspace_id, request.task_id];
  const runtime = useQuery<string>({ queryKey: runtimeKey, queryFn: skipToken, initialData: request.runtime_id || undefined });
  const [chosenRepository, setRepository] = useState<string | null>(() => navigation?.searchParams.get("review_repo") ?? null);
  const [chosenTarget, setTarget] = useState<string | null>(() => navigation?.searchParams.get("review_target") ?? null);
  const [draft, setDraft] = useState<string | null>(null);
  const [comment, setComment] = useState("");
  const [confirmMerge, setConfirmMerge] = useState(false);
  const [indexBusy, setIndexBusy] = useState(false);
  const [selectionBusy, setSelectionBusy] = useState(false);
  const [selectedCommit, setSelectedCommit] = useState("");
  const [inspectorTab, setInspectorTab] = useState<"review" | "commit" | "history">(() => {
    const tab = navigation?.searchParams.get("review_tab");
    return tab === "commit" || tab === "history" ? tab : "review";
  });
  const [selectedPath, setSelectedPath] = useState(() => navigation?.searchParams.get("review_file") ?? "");
  const [diffLayout, setDiffLayout] = useState<ReviewDiffLayout>(() =>
    parseReviewDiffLayout(navigation?.searchParams.get("review_layout") ?? null)
  );
  const [fileFilter, setFileFilter] = useState<ReviewFileFilter>(() =>
    parseReviewFileFilter(navigation?.searchParams.get("review_filter") ?? null)
  );
  const [fileView, setFileView] = useState<ReviewFileView>(() =>
    parseReviewFileView(navigation?.searchParams.get("review_view") ?? null)
  );
  const [mobileInspectorOpen, setMobileInspectorOpen] = useState(false);
  const [commentSide, setCommentSide] = useState<ReviewCommentSide>(() =>
    navigation?.searchParams.get("review_side") === "old" ? "old" : "new"
  );
  const [comments, setComments] = useState<readonly LocalReviewComment[]>([]);
  const [dismissedFindings, setDismissedFindings] = useState<ReadonlySet<string>>(() => new Set());
  const [hydratedCommentsKey, setHydratedCommentsKey] = useState<string | null>(null);
  const [hydratedDismissedKey, setHydratedDismissedKey] = useState<string | null>(null);
  const [replyTo, setReplyTo] = useState<string | null>(null);
  const [replyText, setReplyText] = useState("");
  const [findingApplying, setFindingApplying] = useState<string | null>(null);
  const [findingError, setFindingError] = useState(false);
  const [focusComment, setFocusComment] = useState<LocalReviewComment | null>(null);
  const repositories = useQuery({
    queryKey: ["review-repositories", userId, request.workspace_id, request.task_id, request.path],
    queryFn: async ({ signal }) => {
      const result = await readReviewRepositories({ ...request, action: "repositories", runtime_id: request.runtime_id || runtime.data }, signal);
      if (result.runtime_id) client.setQueryData(runtimeKey, result.runtime_id);
      return result;
    },
    retry: false, staleTime: 30000, refetchOnWindowFocus: false, networkMode: "always",
  });
  const repositoryPath = chosenRepository ?? (repositories.data?.repositories.includes(request.path) ? request.path : repositories.data?.repositories[0]) ?? request.path;
  const scope = { ...request, runtime_id: request.runtime_id || runtime.data, path: repositoryPath };
  const branches = useQuery({
    queryKey: ["local-review-branches", userId, request.workspace_id, request.task_id, repositoryPath],
    queryFn: ({ signal }) => readLocalReviewBranches(scope, signal), enabled: !!repositories.data?.repositories.length,
    retry: false, staleTime: 30000, refetchOnWindowFocus: false, networkMode: "always",
  });
  const target = chosenTarget ?? defaultReviewTarget(branches.data ?? [], chosenRepository ? "" : request.target);
  const targetDraft = draft ?? target;
  const key = ["paged-local-review", userId, request.workspace_id, request.task_id, repositoryPath, target];
  const manifest = useQuery({
    queryKey: key, enabled: !!target,
    queryFn: async ({ signal }) => {
      const result = await readReviewManifest({ ...scope, target, action: "manifest" }, signal);
      if (result.runtime_id) client.setQueryData(runtimeKey, result.runtime_id);
      return result;
    },
    retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false, networkMode: "always",
  });
  const operation = useMutation({
    networkMode: "always",
    mutationFn: (action: LocalReviewDecision) => {
      if (!manifest.data) throw new Error("Load the review version first");
      return readReviewManifest({ ...scope, runtime_id: scope.runtime_id || manifest.data.runtime_id, target, action, version_id: manifest.data.version_id, snapshot_id: manifest.data.version_id, comment });
    },
    onSuccess: (result) => { client.setQueryData(key, result); setConfirmMerge(false); void invalidateWorktreeInventory(client); },
  });
  const data = manifest.data;
  const commentsKey = data
    ? `multica_local_review_comments:${userId}:${request.workspace_id}:${request.task_id}:${repositoryPath}:${data.version_id}`
    : null;
  const dismissedFindingsKey = commentsKey ? `${commentsKey}:dismissed` : null;
  useEffect(() => {
    if (!commentsKey || loadedCommentsKey.current === commentsKey) return;
    loadedCommentsKey.current = commentsKey;
    setComments(parseLocalReviewComments(defaultStorage.getItem(commentsKey)));
    setHydratedCommentsKey(commentsKey);
  }, [commentsKey]);
  useEffect(() => {
    if (!commentsKey || hydratedCommentsKey !== commentsKey) return;
    if (comments.length) defaultStorage.setItem(commentsKey, JSON.stringify(comments));
    else defaultStorage.removeItem(commentsKey);
  }, [comments, commentsKey, hydratedCommentsKey]);
  useEffect(() => {
    if (!dismissedFindingsKey || loadedDismissedKey.current === dismissedFindingsKey) return;
    loadedDismissedKey.current = dismissedFindingsKey;
    const stored = defaultStorage.getItem(dismissedFindingsKey);
    if (!stored) {
      setDismissedFindings(new Set());
      setHydratedDismissedKey(dismissedFindingsKey);
      return;
    }
    try {
      const parsed: unknown = JSON.parse(stored);
      setDismissedFindings(new Set(
        Array.isArray(parsed) ? parsed.filter((id): id is string => typeof id === "string") : [],
      ));
      setHydratedDismissedKey(dismissedFindingsKey);
    } catch (error) {
      if (!(error instanceof SyntaxError)) throw error;
      setDismissedFindings(new Set());
      setHydratedDismissedKey(dismissedFindingsKey);
    }
  }, [dismissedFindingsKey]);
  useEffect(() => {
    if (!dismissedFindingsKey || hydratedDismissedKey !== dismissedFindingsKey) return;
    if (dismissedFindings.size) defaultStorage.setItem(dismissedFindingsKey, JSON.stringify([...dismissedFindings]));
    else defaultStorage.removeItem(dismissedFindingsKey);
  }, [dismissedFindings, dismissedFindingsKey, hydratedDismissedKey]);
  const lease = useQuery({
    queryKey: ["review-lease", userId, request.workspace_id, request.task_id, data?.version_id],
    queryFn: ({ signal }) => {
      if (!data) throw new Error("Review version unavailable");
      return renewReviewLease({ ...scope, action: "lease", runtime_id: scope.runtime_id || data.runtime_id, target: data.header.target, version_id: data.version_id }, signal);
    },
    enabled: !!data, refetchInterval: 60000, refetchIntervalInBackground: true, retry: false, networkMode: "always",
  });
  const pending = repositories.isFetching || branches.isFetching || manifest.isFetching || operation.isPending || indexBusy || selectionBusy;
  const targetChanged = targetDraft !== target;
  const busy = pending || targetChanged;
  const error = operation.error ?? manifest.error ?? branches.error ?? repositories.error ?? lease.error;
  const merged = data?.review.state === "merged";
  const effectiveScope = { ...scope, runtime_id: scope.runtime_id || data?.runtime_id, target };
  const decisionLabels: Record<LocalReviewDecision, string> = {
    submit: t(($) => $.local_review.submit),
    approve: t(($) => $.local_review.approve),
    request_changes: t(($) => $.local_review.request_changes),
    merge: t(($) => $.local_review.merge),
  };
  useEffect(() => {
    const previous = document.activeElement;
    closeRef.current?.focus();
    return () => {
      if (previous instanceof HTMLElement) previous.focus();
    };
  }, []);
  useEffect(() => {
    if (typeof window === "undefined" || typeof window.matchMedia !== "function") return;
    const desktop = window.matchMedia("(min-width: 1024px)");
    const sync = () => {
      if (desktop.matches) setMobileInspectorOpen(false);
    };
    sync();
    desktop.addEventListener("change", sync);
    return () => desktop.removeEventListener("change", sync);
  }, []);
  const navigationPathname = navigation?.pathname ?? "";
  const navigationSearch = navigation?.searchParams.toString() ?? "";
  const replaceNavigation = navigation?.replace;
  useEffect(() => {
    if (!replaceNavigation) return;
    const params = new URLSearchParams(navigationSearch);
    const values = {
      review_repo: repositoryPath,
      review_target: target,
      review_file: selectedPath,
      review_layout: diffLayout,
      review_filter: fileFilter,
      review_view: fileView,
      review_tab: inspectorTab,
      review_side: commentSide,
    } as const;
    for (const [key, value] of Object.entries(values)) {
      if (value) params.set(key, value);
      else params.delete(key);
    }
    const query = params.toString();
    const next = query ? `${navigationPathname}?${query}` : navigationPathname;
    const current = `${navigationPathname}${navigationSearch ? `?${navigationSearch}` : ""}`;
    if (next !== current) replaceNavigation(next);
  }, [
    commentSide,
    diffLayout,
    fileFilter,
    fileView,
    inspectorTab,
    navigationPathname,
    navigationSearch,
    replaceNavigation,
    repositoryPath,
    selectedPath,
    target,
  ]);
  useEffect(() => {
    if (layoutExplicit.current || typeof window === "undefined") return;
    const wide = window.matchMedia("(min-width: 900px)");
    const sync = () => setDiffLayout(wide.matches ? "split" : "unified");
    sync();
    wide.addEventListener("change", sync);
    return () => wide.removeEventListener("change", sync);
  }, []);
  useEffect(() => {
    const focusChanges = (event: KeyboardEvent) => {
      if (
        !(event.metaKey || event.ctrlKey) ||
        !event.shiftKey ||
        event.key.toLowerCase() !== "g"
      ) return;
      event.preventDefault();
      document.getElementById("local-review-changes")?.focus();
    };
    window.addEventListener("keydown", focusChanges);
    return () => window.removeEventListener("keydown", focusChanges);
  }, []);
  const reviewState = data
    ? data.review.state === "open"
      ? t(($) => $.local_review.state_open)
      : t(($) => $.local_review[data.review.state])
    : "";
  const visibleComments = comments.filter((comment) =>
    (!selectedPath || comment.file === selectedPath)
    && (!data || comment.revision === data.version_id)
  );
  const addComment = (comment: LocalReviewComment) => {
    setComments((current) => current.some((entry) => entry.id === comment.id)
      ? current
      : [...current, comment]);
  };
  const addFindings = (findings: readonly LocalReviewComment[]) => {
    setComments((current) => {
      const ids = new Set(current.map((entry) => entry.id));
      const additions = findings.filter((entry) => !ids.has(entry.id) && !dismissedFindings.has(entry.id));
      return additions.length ? [...current, ...additions] : current;
    });
  };
  const toggleComment = (id: string) => {
    setComments((current) => current.map((comment) =>
      comment.id === id ? { ...comment, resolved: !comment.resolved } : comment
    ));
  };
  const dismissComment = (id: string) => {
    setComments((current) => current.filter((comment) => comment.id !== id));
    setDismissedFindings((current) => new Set([...current, id]));
  };
  const replyToComment = (id: string) => {
    const body = replyText.trim();
    if (!body) return;
    setComments((current) => appendLocalReviewReply(current, id, {
      id: createSafeId(),
      body,
      author: "member",
      createdAt: new Date().toISOString(),
    }));
    setReplyText("");
    setReplyTo(null);
  };
  const applyFinding = async (finding: LocalReviewComment) => {
    if (!data || !finding.suggestion) return;
    setFindingApplying(finding.id);
    setFindingError(false);
    try {
      let offset = 0;
      const chunks: string[] = [];
      for (;;) {
        const page = await readReviewContent({
          ...effectiveScope,
          action: "content",
          target: data.header.target,
          version_id: data.version_id,
          file_path: finding.file,
          side: "new",
          offset,
          limit: 65536,
        });
        chunks.push(page.content.text);
        if (!page.content.has_more) break;
        if (page.content.next_offset <= offset) throw new Error("review content did not advance");
        offset = page.content.next_offset;
      }
      const base = chunks.join("");
      const next = applyReviewFindingSuggestion(base, finding);
      if (next === null) throw new Error("finding line is outside the file");
      const result = await applyReviewFileEdit({
        task_id: request.task_id,
        workspace_id: request.workspace_id,
        runtime_id: effectiveScope.runtime_id,
        path: repositoryPath,
        target: data.header.branch,
        relative_path: finding.file,
        command_id: createSafeId(),
        action: "merge_edit",
        edit_base: base,
        edit_new: next,
      });
      if (result?.conflict) throw new Error("file changed before the suggestion was applied");
      setComments((current) => current.map((entry) =>
        entry.id === finding.id ? { ...entry, resolved: true } : entry
      ));
      await manifest.refetch();
      void invalidateWorktreeInventory(client);
    } catch (error) {
      if (error instanceof Error) setFindingError(true);
      else throw error;
    } finally {
      setFindingApplying(null);
    }
  };
  const findingLabels: Record<ReviewFindingKind, string> = {
    debugger: t(($) => $.local_review.finding_debugger),
    console: t(($) => $.local_review.finding_console),
    type_bypass: t(($) => $.local_review.finding_type_bypass),
    todo: t(($) => $.local_review.finding_todo),
    conflict_marker: t(($) => $.local_review.finding_conflict_marker),
    empty_catch: t(($) => $.local_review.finding_empty_catch),
  };
  const severityLabels = {
    high: t(($) => $.local_review.severity_high),
    medium: t(($) => $.local_review.severity_medium),
    low: t(($) => $.local_review.severity_low),
  } as const;
  const handleDialogKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (event.key === "Escape") {
      event.preventDefault();
      if (!operation.isPending && !indexBusy && !selectionBusy) onClose();
      return;
    }
    if (event.key !== "Tab") return;
    const dialog = dialogRef.current;
    if (!dialog) return;
    const focusable = [...dialog.querySelectorAll<HTMLElement>(
      'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
    )].filter((element) => element.getClientRects().length > 0);
    if (!focusable.length) return;
    const first = focusable[0];
    const last = focusable.at(-1);
    if (!first || !last) return;
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  };
  const handleInspectorTabKeyDown = (event: ReactKeyboardEvent<HTMLButtonElement>) => {
    const tabs = ["review", "commit", "history"] as const;
    const currentIndex = tabs.indexOf(inspectorTab);
    const nextIndex = event.key === "Home"
      ? 0
      : event.key === "End"
        ? tabs.length - 1
        : event.key === "ArrowRight"
          ? (currentIndex + 1) % tabs.length
          : event.key === "ArrowLeft"
            ? (currentIndex - 1 + tabs.length) % tabs.length
            : -1;
    if (nextIndex < 0) return;
    event.preventDefault();
    setInspectorTab(tabs[nextIndex] ?? "review");
    event.currentTarget.parentElement
      ?.querySelectorAll<HTMLButtonElement>('[role="tab"]')
      .item(nextIndex)
      .focus();
  };
  return createPortal(
    <div
      ref={dialogRef}
      role="dialog"
      aria-modal="true"
      aria-labelledby="local-review-title"
      tabIndex={-1}
      onKeyDown={handleDialogKeyDown}
      className="fixed inset-0 z-50 flex h-dvh w-dvw flex-col overflow-hidden bg-background text-foreground"
    >
      <header className="flex min-h-14 shrink-0 flex-wrap items-center gap-2 border-b px-3 py-2">
        <Button
          ref={closeRef}
          size="icon-sm"
          variant="ghost"
          aria-label={t(($) => $.local_review.close)}
          disabled={operation.isPending || indexBusy || selectionBusy}
          onClick={onClose}
        >
          <ArrowLeft aria-hidden="true" />
        </Button>
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-center gap-2 text-body font-semibold">
            <GitBranch className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <span id="local-review-title" className="truncate">{data?.header.branch ?? repositoryPath.split("/").at(-1)}</span>
            <span className="text-muted-foreground">→</span>
            <span className="truncate">{target || t(($) => $.local_review.target)}</span>
          </div>
          <p className="truncate text-caption text-muted-foreground" title={repositoryPath}>
            {repositoryPath}
          </p>
        </div>
        {data && (
          <span className="rounded-full border bg-muted/40 px-2 py-1 text-caption">
            {reviewState} · {data.page.total_files} {t(($) => $.local_review.files)} · {t(($) => $.local_review.file_changes, { additions: data.page.additions, deletions: data.page.deletions })}
          </span>
        )}
        <Button
          size="sm"
          variant="outline"
          disabled={pending || !targetDraft}
          onClick={() => {
            setConfirmMerge(false);
            operation.reset();
            if (targetChanged) setTarget(targetDraft);
            else void manifest.refetch();
          }}
        >
          <RefreshCw aria-hidden="true" />
          {targetChanged ? t(($) => $.local_review.apply_target) : t(($) => $.local_review.refresh)}
        </Button>
        {canOpenLocalPath() && (
          <Button
            size="sm"
            variant="ghost"
            onClick={() => void openLocalPath(repositoryPath)}
          >
            <ExternalLink aria-hidden="true" />
            {t(($) => $.local_review.open_ide)}
          </Button>
        )}
        <Button
          size="sm"
          variant="ghost"
          className="lg:hidden"
          aria-expanded={mobileInspectorOpen}
          onClick={() => setMobileInspectorOpen((open) => !open)}
        >
          <MessageSquareText aria-hidden="true" />
          {t(($) => $.local_review.review)}
        </Button>
      </header>

      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b bg-muted/20 px-3 py-2">
        {repositories.data && repositories.data.repositories.length > 1 && (
          <label className="flex min-w-0 items-center gap-2 text-caption">
            <span className="shrink-0 text-muted-foreground">{t(($) => $.local_review.repository)}</span>
            <select
              aria-label={t(($) => $.local_review.repository)}
              value={repositoryPath}
              disabled={operation.isPending}
              className="h-8 max-w-80 min-w-0 rounded-md border bg-background px-2 text-caption"
              onChange={(event) => {
                setRepository(event.target.value);
                setTarget(null);
                setDraft(null);
                setSelectedPath("");
                setConfirmMerge(false);
                operation.reset();
              }}
            >
              {repositories.data.repositories.map((path) => <option key={path} value={path}>{path}</option>)}
            </select>
          </label>
        )}
        <LocalReviewTargetPicker
          branches={branches.data ?? []}
          value={targetDraft}
          disabled={!branches.data?.length || operation.isPending}
          invalid={!targetChanged && !!error && reviewErrorKind(error) === "target"}
          descriptionId={error ? errorId : undefined}
          onChange={(value) => {
            setDraft(value);
            setConfirmMerge(false);
            operation.reset();
          }}
        />
        <Button
          size="sm"
          variant={diffLayout === "split" ? "secondary" : "ghost"}
          aria-pressed={diffLayout === "split"}
          onClick={() => {
            layoutExplicit.current = true;
            setDiffLayout(diffLayout === "split" ? "unified" : "split");
          }}
        >
          <Columns2 aria-hidden="true" />
          {diffLayout === "split" ? t(($) => $.local_review.side_by_side) : t(($) => $.local_review.inline)}
        </Button>
        {data?.header.dirty && (
          <span className="text-caption text-warning">{t(($) => $.local_review.dirty)}</span>
        )}
        {pending && (
          <span role="status" className="ml-auto flex items-center gap-1.5 text-caption text-muted-foreground">
            <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
            {t(($) => $.local_review.loading)}
          </span>
        )}
      </div>

      {error && <div className="shrink-0 px-3 py-2"><LocalReviewError error={error} id={errorId} /></div>}
      {repositories.isSuccess && !repositories.data.repositories.length && <p role="status" className="p-3 text-caption">{t(($) => $.local_review.no_worktrees)}</p>}
      {branches.isSuccess && !branches.data.length && <p role="status" className="p-3 text-caption">{t(($) => $.local_review.no_local_branches)}</p>}
      {branches.data?.length && !target ? <p role="status" className="p-3 text-caption">{t(($) => $.worktree_lifecycle.choose_target)}</p> : null}

      <div className="relative grid min-h-0 flex-1 grid-cols-1 lg:grid-cols-[minmax(0,1fr)_22rem]">
        <main
          aria-hidden={mobileInspectorOpen || undefined}
          inert={mobileInspectorOpen ? true : undefined}
          className="flex min-h-0 min-w-0 flex-col overflow-hidden lg:border-r"
        >
          {data ? (
            <LocalReviewStagingBrowser
              key={data.version_id}
              request={effectiveScope}
              manifest={data}
              disabled={operation.isPending || selectionBusy}
              onBusyChange={setIndexBusy}
              onSelectionBusy={setSelectionBusy}
              selectedPath={selectedPath}
              onSelectPath={setSelectedPath}
              diffLayout={diffLayout}
              fileFilter={fileFilter}
              fileView={fileView}
              onFileFilterChange={setFileFilter}
              onFileViewChange={setFileView}
              comments={comments}
              focusComment={focusComment}
              commentSide={commentSide}
              onCommentSideChange={setCommentSide}
              onAddComment={addComment}
              onFindings={addFindings}
              onSelectedMerged={(commit) => {
                setSelectedCommit(commit);
                void manifest.refetch();
                void invalidateWorktreeInventory(client);
              }}
              onChanged={() => {
                void manifest.refetch();
                void invalidateWorktreeInventory(client);
              }}
            />
          ) : (
            <div className="flex min-h-64 items-center justify-center p-6 text-caption text-muted-foreground">
              {repositories.isPending || branches.isPending ? t(($) => $.local_review.loading) : t(($) => $.local_review.empty)}
            </div>
          )}
        </main>

        {mobileInspectorOpen && (
          <button
            type="button"
            aria-label={t(($) => $.local_review.close)}
            className="absolute inset-0 z-20 bg-black/20 lg:hidden"
            onClick={() => setMobileInspectorOpen(false)}
          />
        )}
        <aside
          className={`${mobileInspectorOpen ? "flex" : "hidden"} absolute inset-x-0 bottom-0 z-30 h-[min(72dvh,34rem)] min-h-0 flex-col overflow-hidden rounded-t-2xl border-t bg-card pb-[env(safe-area-inset-bottom)] shadow-2xl lg:static lg:flex lg:h-auto lg:rounded-none lg:border-t-0 lg:pb-0 lg:shadow-none`}
          aria-label={t(($) => $.local_review.review_inspector)}
        >
          <div className="grid shrink-0 grid-cols-[repeat(3,minmax(0,1fr))_auto] border-b p-1" role="tablist">
            {([
              ["review", t(($) => $.local_review.review), MessageSquareText],
              ["commit", t(($) => $.local_review.commit), FileCode],
              ["history", t(($) => $.local_review.history), History],
            ] as const).map(([id, label, Icon]) => (
              <button
                key={id}
                type="button"
                role="tab"
                aria-selected={inspectorTab === id}
                aria-controls="local-review-inspector-panel"
                tabIndex={inspectorTab === id ? 0 : -1}
                className={`flex items-center justify-center gap-1.5 rounded-md px-2 py-1.5 text-caption ${inspectorTab === id ? "bg-accent text-accent-foreground" : "text-muted-foreground hover:bg-accent/60"}`}
                onClick={() => setInspectorTab(id)}
                onKeyDown={handleInspectorTabKeyDown}
              >
                <Icon className="size-3.5" aria-hidden="true" />
                {label}
              </button>
            ))}
            <Button className="lg:hidden" size="icon-sm" variant="ghost" aria-label={t(($) => $.local_review.close)} onClick={() => setMobileInspectorOpen(false)}>
              <X aria-hidden="true" />
            </Button>
          </div>
          <div id="local-review-inspector-panel" role="tabpanel" className="min-h-0 flex-1 overflow-y-auto p-3">
            {inspectorTab === "review" && (
              <div className="flex min-h-full flex-col gap-3">
                <section>
                  <h3 className="text-caption font-semibold">{t(($) => $.local_review.review)}</h3>
                  <p className="mt-1 text-caption text-muted-foreground">{reviewState}</p>
                  {selectedCommit && <p role="status" className="mt-1 break-all text-caption">{t(($) => $.local_review.selection_complete, { commit: selectedCommit.slice(0, 12) })}</p>}
                  {merged && <p role="status" className="mt-1 break-all text-caption text-success">{t(($) => $.local_review.merged)} {data?.review.merged_commit}</p>}
                </section>
                <label className="space-y-1 text-caption">
                  <span className="font-medium">{t(($) => $.local_review.comment)}</span>
                  <Textarea
                    value={comment}
                    onChange={(event) => setComment(event.target.value)}
                    placeholder={t(($) => $.local_review.comment_placeholder)}
                    maxLength={8000}
                    rows={6}
                    className="min-h-28 resize-y"
                  />
                </label>
                {data && localReviewActions(data.review.state).includes("request_changes") && !comment.trim() && (
                  <p id={requestChangesHintId} className="text-caption text-muted-foreground">
                    {t(($) => $.local_review.request_changes_needs_comment)}
                  </p>
                )}
                {data && (
                  <div className="mt-auto grid gap-2">
                    {localReviewActions(data.review.state).map((action) => action === "merge" ? (
                      <Button key={action} disabled={busy || data.header.dirty || data.header.branch === target} onClick={() => setConfirmMerge(true)}>
                        {decisionLabels[action]}
                      </Button>
                    ) : (
                      <Button
                        key={action}
                        variant={action === "request_changes" ? "outline" : "default"}
                        disabled={busy || (action === "request_changes" && !comment.trim())}
                        aria-describedby={action === "request_changes" && !comment.trim() ? requestChangesHintId : undefined}
                        onClick={() => operation.mutate(action)}
                      >
                        {decisionLabels[action]}
                      </Button>
                    ))}
                  </div>
                )}
                <section aria-label={t(($) => $.local_review.inline_findings)}>
                  <div className="mb-2 flex items-center justify-between gap-2">
                    <h3 className="font-semibold">{t(($) => $.local_review.inline_findings)}</h3>
                    <span className="text-muted-foreground">{visibleComments.length}</span>
                  </div>
                  {findingError && <p role="alert" className="mb-2 text-destructive">{t(($) => $.local_review.apply_finding_failed)}</p>}
                  {visibleComments.length ? (
                    <ol className="space-y-2">
                      {visibleComments.map((entry) => (
                        <li key={entry.id} className="rounded-md border p-2">
                          <div className="flex items-start gap-2">
                            <span className={`mt-1 size-2 shrink-0 rounded-full ${entry.author === "agent" ? "bg-primary" : "bg-warning"}`} aria-hidden="true" />
                            <button
                              type="button"
                              className="min-w-0 flex-1 text-left"
                              onClick={() => {
                                setSelectedPath(entry.file);
                                setCommentSide(entry.side);
                                setFocusComment({ ...entry });
                                setMobileInspectorOpen(false);
                              }}
                            >
                              <span className="block truncate font-mono">{entry.file.split("/").at(-1)}:{entry.line}</span>
                              <span className="block text-muted-foreground">
                                {entry.author === "agent" ? t(($) => $.local_review.agent_finding) : t(($) => $.local_review.member_comment)}
                                {entry.severity ? ` · ${severityLabels[entry.severity]}` : ""}
                              </span>
                            </button>
                          </div>
                          <p className={`mt-1 whitespace-pre-wrap break-words ${entry.resolved ? "line-through text-muted-foreground" : ""}`}>
                            {entry.author === "agent" && entry.kind
                              ? findingLabels[entry.kind]
                              : entry.body}
                          </p>
                          {!!entry.replies.length && (
                            <ol className="mt-2 space-y-1 border-t pt-2">
                              {entry.replies.map((reply) => (
                                <li key={reply.id}>
                                  <p className="text-muted-foreground">
                                    {reply.author === "agent" ? t(($) => $.local_review.agent_finding) : t(($) => $.local_review.member_comment)}
                                    {" · "}
                                    <time dateTime={reply.createdAt}>
                                      {new Date(reply.createdAt).toLocaleTimeString(locale, {
                                        hour: "2-digit",
                                        minute: "2-digit",
                                      })}
                                    </time>
                                  </p>
                                  <p className="whitespace-pre-wrap break-words">{reply.body}</p>
                                </li>
                              ))}
                            </ol>
                          )}
                          {replyTo === entry.id ? (
                            <form
                              className="mt-2 space-y-1"
                              onSubmit={(event) => {
                                event.preventDefault();
                                replyToComment(entry.id);
                              }}
                            >
                              <Textarea
                                autoFocus
                                rows={2}
                                maxLength={4000}
                                value={replyText}
                                onChange={(event) => setReplyText(event.target.value)}
                                placeholder={t(($) => $.local_review.reply_placeholder)}
                              />
                              <div className="flex gap-1">
                                <Button type="submit" size="xs" disabled={!replyText.trim()}>{t(($) => $.local_review.reply)}</Button>
                                <Button type="button" size="xs" variant="ghost" onClick={() => { setReplyTo(null); setReplyText(""); }}>{t(($) => $.local_review.cancel)}</Button>
                              </div>
                            </form>
                          ) : (
                            <Button size="xs" variant="ghost" className="mt-2" onClick={() => { setReplyTo(entry.id); setReplyText(""); }}>
                              {t(($) => $.local_review.reply)}
                            </Button>
                          )}
                          <div className="mt-2 flex gap-1">
                            <Button size="xs" variant="ghost" onClick={() => toggleComment(entry.id)}>
                              {entry.resolved ? t(($) => $.local_review.reopen) : t(($) => $.local_review.resolve)}
                            </Button>
                            {entry.author === "agent" && (
                              <>
                                {entry.suggestion && (
                                  <Button
                                    size="xs"
                                    variant="outline"
                                    disabled={findingApplying === entry.id || !data || data.header.dirty}
                                    onClick={() => void applyFinding(entry)}
                                  >
                                    {findingApplying === entry.id && <Loader2 className="animate-spin" aria-hidden="true" />}
                                    {entry.suggestion.kind === "remove_line"
                                      ? t(($) => $.local_review.finding_remove_line)
                                      : t(($) => $.local_review.finding_replace_line)}
                                  </Button>
                                )}
                                <Button size="xs" variant="ghost" onClick={() => dismissComment(entry.id)}>
                                  {t(($) => $.local_review.dismiss)}
                                </Button>
                              </>
                            )}
                          </div>
                        </li>
                      ))}
                    </ol>
                  ) : (
                    <p className="text-muted-foreground">{t(($) => $.local_review.no_inline_findings)}</p>
                  )}
                </section>
                {confirmMerge && data && (
                  <div role="alert" className="space-y-2 rounded-md border p-3 text-caption">
                    <p className="break-words">{t(($) => $.local_review.branch_comparison, { branch: data.header.branch, head: data.header.head.slice(0, 8), target, targetHead: data.header.target_head.slice(0, 8) })}</p>
                    <dl className="grid grid-cols-2 gap-1 text-muted-foreground">
                      <div><dt>{t(($) => $.local_review.files)}</dt><dd className="font-medium text-foreground">{data.page.total_files}</dd></div>
                      <div><dt>{t(($) => $.local_review.file_filter_short)}</dt><dd className="font-medium text-foreground">{t(($) => $.local_review.file_changes, { additions: data.page.additions, deletions: data.page.deletions })}</dd></div>
                      <div><dt>{t(($) => $.local_review.dirty)}</dt><dd className="font-medium text-foreground">{data.header.dirty ? t(($) => $.local_review.yes) : t(($) => $.local_review.no)}</dd></div>
                      <div><dt>{t(($) => $.local_review.filter_conflicts)}</dt><dd className="font-medium text-foreground">{data.page.files.filter((file) => file.status === "conflicted").length}</dd></div>
                    </dl>
                    <Button size="sm" disabled={busy} onClick={() => operation.mutate("merge")}>{t(($) => $.local_review.confirm_merge)}</Button>
                    <Button size="sm" variant="ghost" onClick={() => setConfirmMerge(false)}>{t(($) => $.local_review.cancel)}</Button>
                  </div>
                )}
              </div>
            )}
            {inspectorTab === "commit" && data && (
              <LocalReviewIndex
                request={effectiveScope}
                disabled={operation.isPending || selectionBusy}
                onBusyChange={setIndexBusy}
                onChanged={() => {
                  void manifest.refetch();
                  void invalidateWorktreeInventory(client);
                }}
                compact
              />
            )}
            {inspectorTab === "history" && data && (
              <div className="space-y-4 text-caption">
                <LocalReviewCommits key={`commits-${data.version_id}`} request={effectiveScope} manifest={data} />
                <section>
                  <h3 className="mb-2 font-semibold">{t(($) => $.local_review.history)}</h3>
                  {data.review.events.length ? (
                    <ol className="space-y-2">
                      {data.review.events.map((event, index) => (
                        <li key={`${event.created_at}:${index}`} className="rounded-md border p-2">
                          <p>{event.actor_name || event.actor_id || t(($) => $.local_review.former_member)} · {event.kind === "approve" ? t(($) => $.local_review.approved) : event.kind === "request_changes" ? t(($) => $.local_review.changes_requested) : event.kind === "merge" || event.kind === "merge_recovered" ? t(($) => $.local_review.merged) : t(($) => $.local_review.submit)}</p>
                          <time dateTime={event.created_at} className="text-muted-foreground">{new Date(event.created_at).toLocaleString(locale)}</time>
                          {event.comment && <p className="mt-1 whitespace-pre-wrap break-words">{event.comment}</p>}
                        </li>
                      ))}
                    </ol>
                  ) : (
                    <p className="text-muted-foreground">{t(($) => $.local_review.no_history)}</p>
                  )}
                </section>
              </div>
            )}
          </div>
        </aside>
      </div>
    </div>,
    document.body,
  );
}

function LocalReviewCommits({ request, manifest }: { request: LocalReviewRequest; manifest: ReviewManifest }) {
  const { t } = useT("issues");
  const errorId = useId();
  const [open, setOpen] = useState(false);
  const commits = useInfiniteQuery({
    queryKey: ["review-version-commits", request.workspace_id, request.task_id, manifest.version_id],
    queryFn: ({ pageParam, signal }) => readReviewCommits({ ...request, action: "commits", version_id: manifest.version_id, offset: pageParam, limit: 50 }, signal),
    initialPageParam: 0, getNextPageParam: (last) => last.has_more ? last.next_offset : undefined,
    enabled: open, retry: false, staleTime: Infinity, refetchOnWindowFocus: false, networkMode: "always",
  });
  return <details className="text-caption" onToggle={(event) => setOpen(event.currentTarget.open)}><summary>{t(($) => $.local_review.commits)}</summary>
    {open && <div className="max-h-28 overflow-auto py-2">
      {commits.isPending && <p role="status">{t(($) => $.local_review.loading)}</p>}
      {commits.error && <LocalReviewError error={commits.error} id={errorId} />}
      {commits.data?.pages.flatMap((page) => page.commits).map((commit) => <p key={commit.sha} className="break-all font-mono">{commit.sha.slice(0, 8)} {commit.subject}</p>)}
      {commits.hasNextPage && <Button variant="outline" disabled={commits.isFetchingNextPage} onClick={() => void commits.fetchNextPage()}>{t(($) => $.local_review.load_more_commits)}</Button>}
    </div>}
  </details>;
}
