import React from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createAuthStore,
  registerAuthStore,
} from "@multica/core/auth";
import { ApiClient, ApiError, setApiInstance } from "@multica/core/api";
import type { DocumentContentVersion } from "@multica/core/api/document-content-version";
import { useDocModeStore } from "@multica/core/chat";
import { I18nProvider } from "@multica/core/i18n/react";
import type { ChatSessionFilesResponse, User } from "@multica/core/types";
import { DocModeSurface } from "../../../packages/views/chat/components/doc-mode-surface";
import en from "../../../packages/views/locales/en/chat.json";
import zh from "../../../packages/views/locales/zh-Hans/chat.json";
import "../../../apps/desktop/src/renderer/src/globals.css";

const params = new URLSearchParams(window.location.search);
const sessionId = "fixture-session";
const issueId = "fixture-issue";
const base = `# Workbench QA

This document validates autosave, checkpoint history, and conflict recovery.

## Acceptance

- Draft survives a refresh
- History stays grouped by session
- Concurrent edits never overwrite silently
`;
const remote = `# Workbench QA

Updated in another window.

## Acceptance

- Draft survives a refresh
- History stays grouped by session
- Concurrent edits never overwrite silently
`;
const now = Date.now();
const version = (
  revision: number,
  content: string,
  input: Partial<DocumentContentVersion> = {},
): DocumentContentVersion => ({
  id: `00000000-0000-4000-8000-${revision.toString().padStart(12, "0")}`,
  owner_type: "issue",
  owner_id: "00000000-0000-4000-8000-000000000001",
  revision,
  owner_revision: revision,
  content_type: "text/markdown",
  content,
  content_url: null,
  size_bytes: new TextEncoder().encode(content).length,
  actor_type: "member",
  actor_id: "00000000-0000-4000-8000-000000000002",
  merged: false,
  session_id: sessionId,
  note: null,
  created_at: new Date(now - (6 - revision) * 60_000).toISOString(),
  ...input,
});
let current = version(5, base);
let versions = [
  current,
  version(4, base, {
    note: "Reviewed outline",
    created_at: new Date(now - 10 * 60_000).toISOString(),
  }),
  version(3, base.replace("Draft survives", "Draft survives offline"), {
    merged: true,
    note: null,
    created_at: new Date(now - 12 * 60_000).toISOString(),
  }),
  version(2, base, {
    session_id: "fixture-old-session",
    note: "Initial draft",
    created_at: new Date(now - 24 * 60 * 60_000).toISOString(),
  }),
  version(1, "# Workbench QA\n\nInitial text.\n", {
    session_id: null,
    note: "Imported document",
    created_at: new Date(now - 25 * 60 * 60_000).toISOString(),
  }),
];
let conflictPending = params.has("conflict");

class DocFixtureApi extends ApiClient {
  override async getIssueContentVersionCurrent() {
    return current;
  }

  override async listIssueContentVersions() {
    return versions;
  }

  override async recordIssueContentVersion(
    _issueId: string,
    input: {
      content?: string | null;
      content_type: string;
      expected_revision?: number;
      merge?: boolean;
      note?: string | null;
      session_id?: string;
    },
  ) {
    if (conflictPending) {
      conflictPending = false;
      current = version(6, remote, {
        owner_revision: 6,
        session_id: "fixture-other-session",
      });
      throw new ApiError("conflict", 409, "Conflict");
    }
    const next = version((current.owner_revision ?? current.revision) + 1, input.content ?? "", {
      merged: input.merge === true,
      note: input.note ?? null,
      session_id: input.session_id ?? sessionId,
      actor_type: "member",
    });
    current = next;
    versions = [next, ...versions];
    return next;
  }

  override async deleteIssueContentVersion(_issueId: string, revision: number) {
    versions = versions.filter((entry) => entry.revision !== revision);
  }

  override async rollbackIssueContentVersion(_issueId: string, revision: number) {
    const target = versions.find((entry) => entry.revision === revision) ?? current;
    return this.recordIssueContentVersion(issueId, {
      content: target.content,
      content_type: target.content_type,
      session_id: sessionId,
      note: `Restored v${revision}`,
    });
  }

  override async listMembers() {
    return [];
  }

  override async listAgents() {
    return [];
  }

  override async listChatSessionFiles(): Promise<ChatSessionFilesResponse> {
    return {
      workspace_id: "fixture-workspace",
      attachments: [],
      tasks: [],
      repos: [],
    };
  }
}

const api = new DocFixtureApi("");
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
    id: "00000000-0000-4000-8000-000000000002",
    name: "Fixture member",
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
useDocModeStore.getState().openDoc(sessionId, {
  kind: "issue",
  id: issueId,
  label: "Workbench QA",
  sessionId,
  contentType: "text/markdown",
});
if (params.has("history")) useDocModeStore.getState().setDiffView(sessionId, true);

const root = document.getElementById("root");
if (!root) throw new Error("fixture root missing");
createRoot(root).render(
  <QueryClientProvider client={new QueryClient()}>
    <I18nProvider
      locale="zh-Hans"
      resources={{
        "zh-Hans": { chat: zh },
        en: { chat: en },
      }}
    >
      <div className="h-dvh w-dvw bg-background text-foreground">
        <DocModeSurface wsId="fixture-workspace" sessionId={sessionId} />
      </div>
    </I18nProvider>
  </QueryClientProvider>,
);
