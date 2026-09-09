import React from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { ApiClient, setApiInstance } from "@multica/core/api";
import type { LocalMR, LocalReviewRequest } from "@multica/core/types/local-review";
import { LocalReviewDialog } from "../../../packages/views/issues/components/local-review-dialog";
import zh from "../../../packages/views/locales/zh-Hans/issues.json";
import en from "../../../packages/views/locales/en/issues.json";
import "../../../apps/desktop/src/renderer/src/globals.css";

const record: LocalMR = {
  id: "fixture-review", workspace_id: "fixture-workspace", runtime_id: "fixture-runtime",
  task_id: "fixture-task", issue_id: null, agent_id: "fixture-agent",
  repository_path: "/runtime/workspaces/CRMEB/feature-review", target_branch: "main", snapshot_id: "snapshot-1",
  state: "open", merged_commit: "", events: [], command_status: "succeeded", command_error: "",
  snapshot: { id: "snapshot-1", path: "/runtime/workspaces/CRMEB/feature-review", branch: "feature/local-mr", target: "main", head: "abcd1234", target_head: "9876abcd", base: "9876abcd", dirty: false, branches: ["main", "feature/local-mr"], commits: "abcd1234 Add local MR flow", files: [
    { path: "src/reviews/确认与合并.ts", status: "tracked", patch: "diff --git a/src/review.ts b/src/review.ts\n--- a/src/review.ts\n+++ b/src/review.ts\n@@ -10,2 +10,3 @@\n export function review() {\n-  return false;\n+  verifySnapshot();\n+  return true;\n" },
    { path: "docs/local-review.md", status: "untracked", patch: "# 本地 MR\n跨机器查看，在所属运行时合并。\n" },
  ] },
};

if (new URLSearchParams(window.location.search).has("history")) {
  record.events = ["merge", "merge_recovered"].map((kind) => ({ kind, snapshot_id: "previous-snapshot", comment: "Completed review round", actor_id: "fixture-user", actor_name: "Reviewer", created_at: "2026-09-08T08:00:00Z" }));
}

// Fixture API never performs network requests or Git writes.
class FixtureAPI extends ApiClient {
  override async supportsLocalMR() { return true; }
  override async executeLocalReview(request: LocalReviewRequest) {
    await this.decideLocalMR(record.id, { action: request.action ?? "read", snapshot_id: request.snapshot_id ?? "" });
    if (!record.snapshot) throw new Error("fixture snapshot missing");
    return { ...record.snapshot, repositories: [record.repository_path], review: { snapshot_id: record.snapshot_id, state: record.state, comment: "", merged_commit: record.merged_commit, events: record.events ?? [] }, history: record.events ?? [] };
  }
  async decideLocalMR(_id: string, request: { action: string; snapshot_id: string; comment?: string }) {
    if (request.action === "approve") record.state = "approved";
    if (request.action === "request_changes") record.state = "changes_requested";
    if (request.action === "merge") { record.state = "merged"; record.merged_commit = "merged-fixture-commit"; }
    if (request.action === "submit") record.state = "open";
  }
}
setApiInstance(new FixtureAPI(""));
const root = document.getElementById("root");
if (!root) throw new Error("fixture root missing");
createRoot(root).render(<QueryClientProvider client={new QueryClient()}><I18nProvider locale="zh-Hans" resources={{ "zh-Hans": { issues: zh }, en: { issues: en } }}>
  <LocalReviewDialog request={{ task_id: record.task_id, workspace_id: record.workspace_id, path: record.repository_path, target: "main" }} onClose={() => {}} />
</I18nProvider></QueryClientProvider>);
