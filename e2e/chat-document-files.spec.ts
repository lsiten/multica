/**
 * E2E coverage for the document-mode contracts added after v1.0.54:
 * session-scoped file projection and member-authored Markdown edits.
 *
 * The fixture uses the real HTTP API and the same PostgreSQL rows that the
 * desktop client reads. It does not start an agent CLI, so failures here are
 * attributable to the chat/file contract rather than an external runtime.
 */
import "./env";
import { expect, test } from "@playwright/test";
import pg from "pg";
import { randomUUID } from "node:crypto";
import { createTestApi } from "./helpers";
import type { TestApiClient } from "./fixtures";

const API_BASE = process.env.NEXT_PUBLIC_API_URL || `http://localhost:${process.env.PORT || "8080"}`;
const DATABASE_URL = process.env.DATABASE_URL ?? "postgres://multica:multica@localhost:5432/multica?sslmode=disable";

async function request(api: TestApiClient, path: string, init: RequestInit = {}) {
  const token = api.getToken();
  if (!token) throw new Error("test api client not logged in");
  return fetch(`${API_BASE}${path}`, {
    ...init,
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
      ...((init.headers as Record<string, string> | undefined) ?? {}),
    },
  });
}

test.describe("Chat document mode", () => {
  let api: TestApiClient;
  let db: pg.Client;
  let sessionId: string;
  let agentId: string;
  let runtimeId: string;
  let attachmentId: string;

  test.beforeEach(async () => {
    api = await createTestApi();
    db = new pg.Client(DATABASE_URL);
    await db.connect();
  });

  test.afterEach(async () => {
    try {
      await db.query("DELETE FROM attachment WHERE id = $1", [attachmentId]);
      await db.query("DELETE FROM chat_session WHERE id = $1", [sessionId]);
      await db.query("DELETE FROM agent WHERE id = $1", [agentId]);
      await db.query("DELETE FROM agent_runtime WHERE id = $1", [runtimeId]);
    } finally {
      await db.end();
      await api.cleanup();
    }
  });

  test("returns session files with stable attachment URLs", async () => {
    const workspace = (await api.getWorkspaces())[0]!;
    api.setWorkspaceId(workspace.id);
    api.setWorkspaceSlug(workspace.slug);
    const user = await db.query<{ id: string }>('SELECT id FROM "user" WHERE email = $1', [api.getEmail()]);
    const userId = user.rows[0]!.id;
    runtimeId = randomUUID();
    agentId = randomUUID();
    sessionId = randomUUID();
    attachmentId = randomUUID();

    await db.query(
      `INSERT INTO agent_runtime (id, workspace_id, daemon_id, name, runtime_mode, provider, status, owner_id, visibility, last_seen_at)
       VALUES ($1, $2, $3, 'E2E document runtime', 'cloud', 'codex', 'online', $4, 'private', now())`,
      [runtimeId, workspace.id, `e2e-${runtimeId}`, userId],
    );
    await db.query(
      `INSERT INTO agent (id, workspace_id, name, runtime_mode, runtime_id, owner_id, visibility, max_concurrent_tasks)
       VALUES ($1, $2, 'E2E document agent', 'cloud', $3, $4, 'private', 1)`,
      [agentId, workspace.id, runtimeId, userId],
    );
    await db.query(
      `INSERT INTO chat_session (id, workspace_id, agent_id, creator_id, title, status)
       VALUES ($1, $2, $3, $4, 'E2E document session', 'active')`,
      [sessionId, workspace.id, agentId, userId],
    );
    await db.query(
      `INSERT INTO chat_message (chat_session_id, role, content, message_kind)
       VALUES ($1, 'user', 'Open the project files', 'message')`,
      [sessionId],
    );
    await db.query(
      `INSERT INTO attachment (id, workspace_id, chat_session_id, uploader_type, uploader_id, filename, url, content_type, size_bytes)
       VALUES ($1, $2, $3, 'member', $4, 'README.md', '/uploads/e2e-readme.md', 'text/markdown', 12)`,
      [attachmentId, workspace.id, sessionId, userId],
    );

    const response = await request(api, `/api/chat/sessions/${sessionId}/files`, {
      headers: {
        "X-Workspace-ID": workspace.id,
        "X-Client-Capabilities": "stable_attachment_urls",
      },
    });
    expect(response.status).toBe(200);
    const body = await response.json() as { attachments: Array<{ id: string; filename: string; download_url: string }> };
    expect(body.attachments).toEqual(expect.arrayContaining([
      expect.objectContaining({
        id: attachmentId,
        filename: "README.md",
        download_url: `/api/attachments/${attachmentId}/download`,
      }),
    ]));
  });

  test("accepts a member Markdown edit and returns the current version", async () => {
    const workspace = (await api.getWorkspaces())[0]!;
    api.setWorkspaceId(workspace.id);
    api.setWorkspaceSlug(workspace.slug);
    const issue = await api.createIssue("E2E editable Markdown", { description: "# Original" });
    const edited = "# Edited\n\nHuman-authored content";
    const write = await request(api, `/api/issues/${issue.id}/content-versions`, {
      method: "POST",
      headers: { "X-Workspace-ID": workspace.id },
      body: JSON.stringify({ content_type: "text/markdown", content: edited, size_bytes: Buffer.byteLength(edited) }),
    });
    expect(write.status).toBe(201);
    const current = await request(api, `/api/issues/${issue.id}/content-versions/current`, {
      headers: { "X-Workspace-ID": workspace.id },
    });
    expect(current.status).toBe(200);
    await expect(current.json()).resolves.toEqual(expect.objectContaining({ content: edited, content_type: "text/markdown" }));
  });
});
