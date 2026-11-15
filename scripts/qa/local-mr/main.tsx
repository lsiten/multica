import React from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createAuthStore,
  registerAuthStore,
} from "@multica/core/auth";
import { ApiClient, setApiInstance } from "@multica/core/api";
import type {
  PagedReviewInput,
  ReviewFilePage,
  ReviewManifest,
} from "@multica/core/types/local-review-pages";
import type { User } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import { LocalReviewDialog } from "../../../packages/views/issues/components/local-review-dialog";
import zh from "../../../packages/views/locales/zh-Hans/issues.json";
import en from "../../../packages/views/locales/en/issues.json";
import "../../../apps/desktop/src/renderer/src/globals.css";

const versionId = "a".repeat(64);
const indexId = "b".repeat(64);
const head = "1".repeat(40);
const targetHead = "2".repeat(40);
const repository = "/runtime/workspaces/CRMEB/feature-review";
const files = [
  {
    path: "src/reviews/确认与合并.ts",
    status: "modified",
    preview: "text" as const,
    additions: 3,
    deletions: 1,
  },
  {
    path: "docs/local-review.md",
    status: "untracked",
    preview: "text" as const,
    additions: 2,
    deletions: 0,
  },
];
let reviewState: "draft" | "open" | "approved" | "changes_requested" | "merged" =
  "open";
let mergedCommit = "";
let events: Array<{
  kind: string;
  actor_id: string;
  actor_name: string;
  comment: string;
  created_at: string;
}> = [];

const patchPage = (input: PagedReviewInput): ReviewFilePage => ({
  version_id: versionId,
  runtime_id: "fixture-runtime",
  path: input.file_path ?? "",
  preview: "text",
  page: {
    lines: [
      { text: "export function review() {", kind: "context", old_line: 1, new_line: 1 },
      { text: "  return false;", kind: "remove", old_line: 2 },
      { text: "  verifySnapshot();", kind: "add", new_line: 2 },
      { text: "  return true;", kind: "add", new_line: 3 },
      { text: "  console.debug('review complete');", kind: "add", new_line: 4 },
      { text: "}", kind: "context", old_line: 3, new_line: 5 },
    ],
    next_line: 6,
    has_more: false,
  },
});

const manifest = (input: PagedReviewInput): ReviewManifest => ({
  version_id: versionId,
  runtime_id: "fixture-runtime",
  header: {
    repository,
    branch: "feature/local-mr",
    target: input.target || "main",
    head,
    target_head: targetHead,
    base: targetHead,
    dirty: false,
    committed: false,
  },
  page: {
    files,
    total_files: files.length,
    next_offset: files.length,
    has_more: false,
    additions: 5,
    deletions: 1,
  },
  review: {
    snapshot_id: versionId,
    state: reviewState,
    comment: "",
    merged_commit: mergedCommit,
    events,
  },
});

class FixtureAPI extends ApiClient {
  override async supportsLocalMR() {
    return true;
  }

  override async supportsPagedLocalMR() {
    return true;
  }

  override async supportsLocalIndex() {
    return true;
  }

  override async supportsSelectedMerge() {
    return true;
  }

  override async listLocalReviewBranches() {
    return ["main", "release"];
  }

  override async applyRuntimeFileEdit() {
    return { conflict: false };
  }

  override async executePagedLocalReview(input: PagedReviewInput) {
    const action = input.action ?? "manifest";
    if (action === "repositories") {
      return { repositories: [repository], runtime_id: "fixture-runtime" };
    }
    if (action === "commits") {
      return {
        version_id: versionId,
        runtime_id: "fixture-runtime",
        commits: [{ sha: head, subject: "Add local MR review workspace" }],
        next_offset: 1,
        has_more: false,
      };
    }
    if (action === "lease") {
      return {
        version_id: versionId,
        runtime_id: "fixture-runtime",
        expires_at: new Date(Date.now() + 60_000).toISOString(),
      };
    }
    if (action === "file" || action === "context") {
      return patchPage(input);
    }
    if (action === "content") {
      const text =
        "export function review() {\n  verifySnapshot();\n  return true;\n  console.debug('review complete');\n}\n";
      const bytes = new TextEncoder().encode(text).length;
      return {
        version_id: versionId,
        runtime_id: "fixture-runtime",
        path: input.file_path ?? "",
        side: input.side ?? "new",
        content: {
          text,
          encoding: "utf8",
          offset: 0,
          next_offset: bytes,
          size: bytes,
          has_more: false,
        },
      };
    }
    if (action === "index") {
      return {
        kind: "index",
        version_id: versionId,
        staged_version_id: versionId,
        unstaged_version_id: versionId,
        runtime_id: "fixture-runtime",
        status: {
          index_id: indexId,
          branch: "feature/local-mr",
          head,
          files: files.map((file) => ({
            path: file.path,
            index_code: file.status === "untracked" ? "?" : "M",
            working_code: file.status === "untracked" ? "?" : " ",
            staged: false,
            unstaged: true,
            untracked: file.status === "untracked",
            conflicted: false,
            unsupported: false,
          })),
        },
      };
    }
    if (action === "stage" || action === "unstage" || action === "commit") {
      if (action === "commit") reviewState = "open";
      return {
        kind: "index_result",
        runtime_id: "fixture-runtime",
        result: {
          index_id: indexId,
          ...(action === "commit"
            ? {
                commit: {
                  commit: "3".repeat(40),
                  parent: head,
                  tree: "4".repeat(40),
                  branch: "feature/local-mr",
                  index_id: indexId,
                },
              }
            : {}),
        },
      };
    }
    if (action === "merge_selected") {
      return {
        kind: "selected_merge",
        version_id: versionId,
        target: input.target || "main",
        paths: input.paths ?? [],
        commit: "5".repeat(40),
        conflicts: [],
        runtime_id: "fixture-runtime",
      };
    }
    if (
      action === "submit" ||
      action === "approve" ||
      action === "request_changes" ||
      action === "merge"
    ) {
      reviewState =
        action === "approve"
          ? "approved"
          : action === "request_changes"
            ? "changes_requested"
            : action === "merge"
              ? "merged"
              : "open";
      if (action === "merge") mergedCommit = "6".repeat(40);
      events = [
        ...events,
        {
          kind: action,
          actor_id: "fixture-user",
          actor_name: "Fixture reviewer",
          comment: input.comment ?? "",
          created_at: new Date().toISOString(),
        },
      ];
    }
    return manifest(input);
  }
}

const api = new FixtureAPI("");
setApiInstance(api);
const auth = createAuthStore({
  api,
  storage: {
    getItem: () => null,
    setItem: () => {},
    removeItem: () => {},
  },
});
auth.setState({
  user: {
    id: "fixture-user",
    name: "Fixture user",
    email: "fixture@example.test",
    avatar_url: null,
    onboarded_at: "2026-01-01T00:00:00Z",
    onboarding_questionnaire: {},
    language: "zh-Hans",
    profile_description: "",
    timezone: "Asia/Shanghai",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  } satisfies User,
  isLoading: false,
  status: "authenticated",
  expired: false,
});
registerAuthStore(auth);

const root = document.getElementById("root");
if (!root) throw new Error("fixture root missing");
createRoot(root).render(
  <QueryClientProvider client={new QueryClient()}>
    <I18nProvider
      locale="zh-Hans"
      resources={{
        "zh-Hans": { issues: zh },
        en: { issues: en },
      }}
    >
      <LocalReviewDialog
        request={{
          task_id: "fixture-task",
          workspace_id: "fixture-workspace",
          path: repository,
          target: "main",
        }}
        onClose={() => {}}
      />
    </I18nProvider>
  </QueryClientProvider>,
);
