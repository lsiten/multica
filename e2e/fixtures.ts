/**
 * TestApiClient — lightweight API helper for E2E test data setup/teardown.
 *
 * Uses raw fetch so E2E tests have zero build-time coupling to the web app.
 */

import "./env";
import pg from "pg";
import { createHash, randomBytes, randomUUID } from "node:crypto";

// `||` (not `??`) so an empty `NEXT_PUBLIC_API_URL=` in .env still falls
// back to localhost. dotenv sets unset-vs-empty both as "" — treating them
// the same matches user intent.
const API_BASE = process.env.NEXT_PUBLIC_API_URL || `http://localhost:${process.env.PORT || "8080"}`;
const DATABASE_URL = process.env.DATABASE_URL ?? "postgres://multica:multica@localhost:5432/multica?sslmode=disable";

interface TestWorkspace {
  id: string;
  name: string;
  slug: string;
}

export type TestIssueStatus =
  | "backlog"
  | "todo"
  | "in_progress"
  | "in_review"
  | "done"
  | "blocked"
  | "cancelled";

export type TestIssuePriority = "urgent" | "high" | "medium" | "low" | "none";

export interface TestTableIssueSeed {
  title: string;
  status?: TestIssueStatus;
  priority?: TestIssuePriority;
  parentIssueId?: string | null;
  position?: number;
}

export interface TestTableIssue {
  id: string;
  title: string;
  status: TestIssueStatus;
  number: number;
}

export class TestApiClient {
  private token: string | null = null;
  private workspaceSlug: string | null = null;
  private workspaceId: string | null = null;
  private email: string | null = null;
  private createdIssueIds: string[] = [];
  private createdProjectIds: string[] = [];
  private createdApplicationIds: string[] = [];
  private seededIssueIds: string[] = [];
  private dependencyIds: string[] = [];
  private humanRequestSources: { taskId: string; agentId: string; runtimeId: string }[] = [];

  /** Seed a disconnected test runtime, then deliver through the actual task-token API. */
  private async createRunFixture(issueId: string | null, chatSessionId: string | null = null) {
    if (!this.workspaceId || !this.email) throw new Error("Fixture requires a logged-in workspace");
    const source = { taskId: randomUUID(), agentId: randomUUID(), runtimeId: randomUUID() };
    const token = `mat_${randomBytes(20).toString("hex")}`;
    const client = new pg.Client(DATABASE_URL);
    await client.connect();
    try {
      const user = await client.query<{ id: string }>('SELECT id FROM "user" WHERE email=$1', [this.email]);
      const userId = user.rows[0]?.id;
      if (!userId) throw new Error("Fixture member was not found");
      await client.query("BEGIN");
      await client.query("INSERT INTO agent_runtime(id,workspace_id,daemon_id,name,runtime_mode,provider,status,owner_id,visibility,last_seen_at) VALUES($1,$2,$3,'E2E approval runtime','local','codex','online',$4,'private',now())", [source.runtimeId, this.workspaceId, `fixture-${source.runtimeId}`, userId]);
      await client.query("INSERT INTO agent(id,workspace_id,name,runtime_mode,runtime_id,owner_id,permission_mode,status) VALUES($1,$2,'E2E request agent ' || $1::uuid::text,'local',$3,$4,'private','idle')", [source.agentId, this.workspaceId, source.runtimeId, userId]);
      await client.query("INSERT INTO agent_task_queue(id,agent_id,runtime_id,issue_id,chat_session_id,status,priority,originator_user_id,accountable_user_id,started_at) VALUES($1,$2,$3,$4,$6,'running',2,$5,$5,now())", [source.taskId, source.agentId, source.runtimeId, issueId, userId, chatSessionId]);
      await client.query("INSERT INTO task_token(token_hash,task_id,agent_id,workspace_id,user_id,expires_at) VALUES($1,$2,$3,$4,$5,now()+interval '1 hour')", [createHash("sha256").update(token).digest("hex"), source.taskId, source.agentId, this.workspaceId, userId]);
      await client.query("COMMIT");
      this.humanRequestSources.push(source);
    } catch (error) {
      await client.query("ROLLBACK");
      throw error;
    } finally {
      await client.end();
    }
    return { ...source, token, userId: (await this.fixtureUserId()) };
  }

  async createHumanRequestFixture(issueId: string | null, payload: Record<string, unknown>, chatSessionId: string | null = null) {
    const { token } = await this.createRunFixture(issueId, chatSessionId);
    const response = await fetch(`${API_BASE}/api/human-requests/`, { method: "POST", headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}`, "X-Workspace-ID": this.workspaceId }, body: JSON.stringify(payload) });
    if (!response.ok) throw new Error(`Request fixture failed: ${response.status} ${await response.text()}`);
    const request = await response.json() as { id: string; revision: number };
    return request;
  }

  private async fixtureUserId() {
    const client = new pg.Client(DATABASE_URL); await client.connect();
    try { const result = await client.query<{id:string}>('SELECT id FROM "user" WHERE email=$1',[this.email]); return result.rows[0]!.id; }
    finally { await client.end(); }
  }

  async createProgressFixture(issueId: string, status: "completed" | "failed", step?: { kind: string; summary: string; missing?: string[]; evidence?: string[] }) {
    const source = await this.createRunFixture(issueId);
    const client = new pg.Client(DATABASE_URL); await client.connect();
    try {
      const current = await client.query<{revision:string}>("UPDATE issue SET assignee_type='agent',assignee_id=$2,revision=revision+1 WHERE id=$1 RETURNING revision",[issueId,source.agentId]);
      const revision = Number(current.rows[0]!.revision);
      if (step) {
        const response = await fetch(`${API_BASE}/api/issues/${issueId}/next-step`, {method:"PUT",headers:{"Content-Type":"application/json",Authorization:`Bearer ${source.token}`,"X-Workspace-ID":this.workspaceId!},body:JSON.stringify({...step,actor_type:"member",actor_id:source.userId,issue_revision:revision})});
        if (!response.ok) throw new Error(`Handoff fixture failed: ${response.status} ${await response.text()}`);
      }
      await client.query("UPDATE agent_task_queue SET status=$2,completed_at=now(),result=$3::jsonb WHERE id=$1",[source.taskId,status,JSON.stringify({summary:"Focused checks passed; delivery available for inspection."})]);
      return {...source,revision};
    } finally { await client.end(); }
  }

  async createChatChoiceFixture(payload: Record<string, unknown>) {
    const source = await this.createRunFixture(null);
    const chatId=randomUUID(); const client = new pg.Client(DATABASE_URL); await client.connect();
    try {
      await client.query("INSERT INTO chat_session(id,workspace_id,creator_id,agent_id,title,status) VALUES($1,$2,$3,$4,'E2E bound choice','active')",[chatId,this.workspaceId,source.userId,source.agentId]);
      await client.query("UPDATE agent_task_queue SET chat_session_id=$2 WHERE id=$1",[source.taskId,chatId]);
    } finally { await client.end(); }
    const response=await fetch(`${API_BASE}/api/human-requests/`,{method:"POST",headers:{"Content-Type":"application/json",Authorization:`Bearer ${source.token}`,"X-Workspace-ID":this.workspaceId!},body:JSON.stringify(payload)});
    if (!response.ok) throw new Error(`Chat request fixture failed: ${response.status} ${await response.text()}`);
    return {chatId, request: await response.json() as {id:string; revision:number}};
  }

  async reviseHumanRequestFixture(id: string, expired = false) {
    const client = new pg.Client(DATABASE_URL); await client.connect();
    try {
      const result = await client.query("UPDATE human_request SET revision=revision+1,expires_at=CASE WHEN $4 THEN now()-interval '1 second' ELSE expires_at END WHERE id=$1 AND workspace_id=$2 AND source_task_id=ANY($3::uuid[])",[id,this.workspaceId,this.humanRequestSources.map(source=>source.taskId),expired]);
      if (result.rowCount!==1) throw new Error("Request is not owned by this fixture");
    } finally {await client.end();}
  }

  async fixtureRuns(issueId: string) {
    const client=new pg.Client(DATABASE_URL);await client.connect();
    try { return (await client.query<{id:string;force_fresh_session:boolean;rerun_of_task_id:string|null;context:Record<string,unknown>}>("SELECT id,force_fresh_session,rerun_of_task_id,context FROM agent_task_queue WHERE issue_id=$1 ORDER BY created_at,id",[issueId])).rows; }
    finally { await client.end(); }
  }

  async login(email: string, name: string) {
    const client = new pg.Client(DATABASE_URL);
    await client.connect();
    try {
      // Keep each E2E login isolated so previous test runs do not trip the
      // per-email send-code rate limit.
      await client.query("DELETE FROM verification_code WHERE email = $1", [email]);

      // Step 1: Send verification code
      const sendRes = await fetch(`${API_BASE}/auth/send-code`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email }),
      });
      if (!sendRes.ok) {
        throw new Error(`send-code failed: ${sendRes.status}`);
      }

      // Step 2: Read code from database
      const result = await client.query(
        "SELECT code FROM verification_code WHERE email = $1 AND used = FALSE AND expires_at > now() ORDER BY created_at DESC LIMIT 1",
        [email],
      );
      if (result.rows.length === 0) {
        throw new Error(`No verification code found for ${email}`);
      }

      const configuredDevCode = process.env.MULTICA_DEV_VERIFICATION_CODE?.trim();
      const code = configuredDevCode || result.rows[0].code;

      // Step 3: Verify code to get JWT
      const verifyRes = await fetch(`${API_BASE}/auth/verify-code`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, code }),
      });
      if (!verifyRes.ok) {
        throw new Error(`verify-code failed: ${verifyRes.status}`);
      }
      const data = await verifyRes.json();

      this.token = data.token;
      this.email = email;

      // Update user name if needed
      if (name && data.user?.name !== name) {
        await this.authedFetch("/api/me", {
          method: "PATCH",
          body: JSON.stringify({ name }),
        });
      }

      await client.query("DELETE FROM verification_code WHERE email = $1", [email]);

      return data;
    } finally {
      await client.end();
    }
  }

  async getWorkspaces(): Promise<TestWorkspace[]> {
    const res = await this.authedFetch("/api/workspaces");
    return res.json();
  }

  setWorkspaceId(id: string) {
    this.workspaceId = id;
  }

  setWorkspaceSlug(slug: string) {
    this.workspaceSlug = slug;
  }

  async ensureWorkspace(name = "E2E Workspace", slug = "e2e-workspace") {
    const workspaces = await this.getWorkspaces();
    const workspace = workspaces.find((item) => item.slug === slug) ?? workspaces[0];
    if (workspace) {
      this.workspaceId = workspace.id;
      this.workspaceSlug = workspace.slug;
      return workspace;
    }

    const res = await this.authedFetch("/api/workspaces", {
      method: "POST",
      body: JSON.stringify({ name, slug }),
    });
    if (res.ok) {
      const created = (await res.json()) as TestWorkspace;
      this.workspaceId = created.id;
      this.workspaceSlug = created.slug;
      return created;
    }

    const refreshed = await this.getWorkspaces();
    const created = refreshed.find((item) => item.slug === slug) ?? refreshed[0];
    if (created) {
      this.workspaceId = created.id;
      this.workspaceSlug = created.slug;
      return created;
    }

    throw new Error(`Failed to ensure workspace ${slug}: ${res.status} ${res.statusText}`);
  }

  async markUserOnboarded() {
    if (!this.email) {
      throw new Error("Cannot mark E2E user onboarded before login");
    }

    const client = new pg.Client(DATABASE_URL);
    await client.connect();
    try {
      const result = await client.query(
        `
          UPDATE "user"
          SET
            onboarded_at = COALESCE(onboarded_at, now()),
            onboarding_questionnaire = COALESCE(onboarding_questionnaire, '{}'::jsonb)
              || '{"source":["friends_colleagues"],"source_other":null,"source_skipped":false}'::jsonb
          WHERE email = $1
        `,
        [this.email],
      );
      if (result.rowCount !== 1) {
        throw new Error(`Failed to mark E2E user onboarded: ${this.email}`);
      }
    } finally {
      await client.end();
    }
  }

  async createApplicationProjectResource(projectId: string) {
    const response = await this.authedFetch(`/api/projects/${projectId}/resources`, { method: "POST", body: JSON.stringify({ resource_type: "github_repo", resource_ref: { url: "https://github.com/example/application" }, label: "Application source" }) });
    if (!response.ok) throw new Error(`create application resource failed: ${response.status}`);
    return await response.json() as { id: string };
  }

  /** Create an application and register its relationships for cleanup. */
  async createApplication(projectId: string, name: string, kind: "service" | "composition", config: Record<string, unknown>) {
    const response = await this.authedFetch("/api/applications/", { method: "POST", body: JSON.stringify({ project_id: projectId, name, kind, config }) });
    if (!response.ok) throw new Error(`create application failed: ${response.status} ${await response.text()}`);
    const application = await response.json() as { id: string; revision: number };
    this.createdApplicationIds.push(application.id);
    return application;
  }

  trackApplication(id: string) { this.createdApplicationIds.push(id); }

  async applicationRequest(path: string, init?: RequestInit) {
    const response = await this.authedFetch(`/api/applications${path}`, init);
    if (!response.ok) throw new Error(`application request failed: ${response.status} ${await response.text()}`);
    return response;
  }

  /** Create a project and register it for cleanup. */
  async createProject(title: string, opts?: Record<string, unknown>) {
    const res = await this.authedFetch("/api/projects", {
      method: "POST",
      body: JSON.stringify({ title, ...opts }),
    });
    if (!res.ok) {
      throw new Error(`create project failed: ${res.status} ${await res.text()}`);
    }
    const project = await res.json();
    this.createdProjectIds.push(project.id);
    return project as { id: string; title: string; status: string };
  }

  async updateProject(id: string, updates: Record<string, unknown>) {
    const res = await this.authedFetch(`/api/projects/${id}`, {
      method: "PUT",
      body: JSON.stringify(updates),
    });
    if (!res.ok) {
      throw new Error(`update project failed: ${res.status} ${await res.text()}`);
    }
    return res.json();
  }

  async deleteProject(id: string) {
    await this.authedFetch(`/api/projects/${id}`, { method: "DELETE" });
  }

  async createIssue(title: string, opts?: Record<string, unknown>) {
    const res = await this.authedFetch("/api/issues", {
      method: "POST",
      body: JSON.stringify({ title, ...opts }),
    });
    const issue = await res.json();
    this.createdIssueIds.push(issue.id);
    return issue;
  }

  /**
   * Insert a large, deterministic issue fixture in one transaction.
   *
   * Browser E2E coverage for cursor-backed Table views needs 1,000+ rows,
   * which would make setup itself dominate the test if every row went through
   * the HTTP create endpoint. These rows intentionally contain no dependent
   * records; cleanup deletes exactly the returned IDs from the isolated E2E
   * workspace.
   */
  async seedTableIssues(rows: TestTableIssueSeed[]): Promise<TestTableIssue[]> {
    if (rows.length === 0) return [];
    if (!this.workspaceId || !this.email) {
      throw new Error("Cannot seed table issues before login and workspace setup");
    }

    const client = new pg.Client(DATABASE_URL);
    await client.connect();
    try {
      await client.query("BEGIN");
      const userResult = await client.query<{ id: string }>(
        `SELECT id FROM "user" WHERE email = $1`,
        [this.email],
      );
      const creatorId = userResult.rows[0]?.id;
      if (!creatorId) {
        throw new Error(`Cannot resolve E2E creator for ${this.email}`);
      }

      const counterResult = await client.query<{ issue_counter: number }>(
        `
          UPDATE workspace
          SET issue_counter = issue_counter + $2
          WHERE id = $1
          RETURNING issue_counter
        `,
        [this.workspaceId, rows.length],
      );
      const finalCounter = Number(counterResult.rows[0]?.issue_counter);
      if (!Number.isFinite(finalCounter)) {
        throw new Error(`Cannot reserve issue numbers for workspace ${this.workspaceId}`);
      }
      const firstNumber = finalCounter - rows.length + 1;

      const inserted = await client.query<TestTableIssue>(
        `
          INSERT INTO issue (
            workspace_id,
            title,
            status,
            priority,
            creator_type,
            creator_id,
            parent_issue_id,
            position,
            number
          )
          SELECT
            $1::uuid,
            fixture.title,
            fixture.status,
            fixture.priority,
            'member',
            $2::uuid,
            fixture.parent_issue_id,
            fixture.position,
            fixture.number
          FROM unnest(
            $3::text[],
            $4::text[],
            $5::text[],
            $6::uuid[],
            $7::double precision[],
            $8::integer[]
          ) WITH ORDINALITY AS fixture(
            title,
            status,
            priority,
            parent_issue_id,
            position,
            number,
            ordinal
          )
          ORDER BY fixture.ordinal
          RETURNING id, title, status, number
        `,
        [
          this.workspaceId,
          creatorId,
          rows.map((row) => row.title),
          rows.map((row) => row.status ?? "backlog"),
          rows.map((row) => row.priority ?? "none"),
          rows.map((row) => row.parentIssueId ?? null),
          rows.map((row, index) => row.position ?? index + 1),
          rows.map((_row, index) => firstNumber + index),
        ],
      );
      await client.query("COMMIT");
      this.seededIssueIds.push(...inserted.rows.map((row) => row.id));
      return inserted.rows;
    } catch (error) {
      await client.query("ROLLBACK");
      throw error;
    } finally {
      await client.end();
    }
  }

  async deleteIssue(id: string) {
    await this.authedFetch(`/api/issues/${id}`, { method: "DELETE" });
  }

  async createComment(issueId: string, content: string) {
    const res = await this.authedFetch(`/api/issues/${issueId}/comments`, {
      method: "POST",
      body: JSON.stringify({ content }),
    });
    if (!res.ok) {
      throw new Error(`create comment failed: ${res.status} ${await res.text()}`);
    }
    return (await res.json()) as { id: string };
  }

  /** Adds an existing user to this client's workspace and returns the member id. */
  async addMemberByEmail(email: string, role: "admin" | "member" = "member"): Promise<string> {
    if (!this.workspaceId) throw new Error("Cannot add a member before a workspace is selected");
    const client = new pg.Client(DATABASE_URL);
    await client.connect();
    try {
      const result = await client.query(
        `INSERT INTO member (workspace_id, user_id, role)
         SELECT $1, id, $3 FROM "user" WHERE email = $2
         RETURNING id`,
        [this.workspaceId, email, role],
      );
      if (result.rowCount !== 1) throw new Error(`No user with email ${email}`);
      return result.rows[0].id as string;
    } finally {
      await client.end();
    }
  }

  async removeMember(memberId: string) {
    const res = await this.authedFetch(`/api/workspaces/${this.workspaceId}/members/${memberId}`, {
      method: "DELETE",
    });
    if (!res.ok) {
      throw new Error(`remove member failed: ${res.status} ${await res.text()}`);
    }
  }

  /** Deletes this client's workspace; only for workspaces a test created. */
  async deleteWorkspace() {
    if (!this.workspaceId) return;
    await this.authedFetch(`/api/workspaces/${this.workspaceId}`, { method: "DELETE" });
  }

  /** Server-side issue search, for comparing against what the UI shows. */
  async searchIssues(q: string, opts: { includeClosed?: boolean; limit?: number } = {}) {
    const params = new URLSearchParams({ q, limit: String(opts.limit ?? 20) });
    if (opts.includeClosed) params.set("include_closed", "true");
    const res = await this.authedFetch(`/api/issues/search?${params}`);
    if (!res.ok) {
      throw new Error(`search issues failed: ${res.status} ${await res.text()}`);
    }
    return (await res.json()) as { issues: { id: string; identifier: string; title: string }[] };
  }

  async updateIssue(id: string, updates: Record<string, unknown>) {
    const res = await this.authedFetch(`/api/issues/${id}`, {
      method: "PUT",
      body: JSON.stringify(updates),
    });
    if (!res.ok) {
      throw new Error(`update issue failed: ${res.status} ${await res.text()}`);
    }
    return res.json();
  }

  async createIssueDependency(issueId: string, prerequisiteId: string) {
    if (!this.workspaceId) throw new Error("Dependency fixture requires a workspace");
    const client = new pg.Client(DATABASE_URL);
    await client.connect();
    try {
      const result = await client.query<{ id: string }>(
        "INSERT INTO issue_dependency(issue_id,depends_on_issue_id,type) SELECT source.id,target.id,'blocked_by' FROM issue source JOIN issue target ON target.workspace_id=source.workspace_id WHERE source.id=$1 AND target.id=$2 AND source.workspace_id=$3 RETURNING id",
        [issueId, prerequisiteId, this.workspaceId],
      );
      const id = result.rows[0]?.id;
      if (!id) throw new Error("Dependency fixture targets must be in this workspace");
      this.dependencyIds.push(id);
    } finally {
      await client.end();
    }
  }

  /** Clean up all issues created during this test. */
  async cleanup() {
    for (const id of this.createdApplicationIds) {
      const response = await this.authedFetch(`/api/applications/${id}`);
      if (!response.ok) continue;
      const application = await response.json() as { revision: number };
      const cleared = await this.authedFetch(`/api/applications/${id}`, { method: "PATCH", body: JSON.stringify({ revision: application.revision, relations: [] }) });
      if (!cleared.ok) throw new Error(`clear application relationships failed: ${cleared.status}`);
    }
    for (const id of this.createdApplicationIds) {
      const response = await this.authedFetch(`/api/applications/${id}`);
      if (!response.ok) continue;
      const application = await response.json() as { revision: number };
      const deleted = await this.authedFetch(`/api/applications/${id}?revision=${application.revision}`, { method: "DELETE" });
      if (!deleted.ok) throw new Error(`delete application fixture failed: ${deleted.status}`);
    }
    this.createdApplicationIds = [];
    if (this.dependencyIds.length > 0) {
      const client = new pg.Client(DATABASE_URL);
      await client.connect();
      try { await client.query("DELETE FROM issue_dependency WHERE id=ANY($1::uuid[])", [this.dependencyIds]); }
      finally { await client.end(); }
      this.dependencyIds = [];
    }
    if (this.seededIssueIds.length > 0 && this.workspaceId) {
      const client = new pg.Client(DATABASE_URL);
      await client.connect();
      try {
        await client.query(
          `DELETE FROM issue WHERE workspace_id = $1 AND id = ANY($2::uuid[])`,
          [this.workspaceId, this.seededIssueIds],
        );
      } finally {
        await client.end();
      }
      this.seededIssueIds = [];
    }
    for (const id of this.createdIssueIds) {
      try {
        await this.deleteIssue(id);
      } catch {
        /* ignore — may already be deleted */
      }
    }
    this.createdIssueIds = [];
    if (this.humanRequestSources.length) {
      const client = new pg.Client(DATABASE_URL);
      await client.connect();
      try {
        for (const source of this.humanRequestSources) {
          await client.query("DELETE FROM inbox_item WHERE details->>'human_request_id' IN (SELECT id::text FROM human_request WHERE source_task_id=$1)", [source.taskId]);
          await client.query("DELETE FROM human_request WHERE source_task_id=$1", [source.taskId]);
          await client.query("DELETE FROM task_token WHERE agent_id=$1", [source.agentId]);
          await client.query("DELETE FROM chat_message WHERE chat_session_id IN (SELECT id FROM chat_session WHERE agent_id=$1)",[source.agentId]);
          await client.query("DELETE FROM chat_session WHERE agent_id=$1",[source.agentId]);
          await client.query("DELETE FROM agent_task_queue WHERE agent_id=$1", [source.agentId]);
          await client.query("DELETE FROM agent WHERE id=$1", [source.agentId]);
          await client.query("DELETE FROM agent_runtime WHERE id=$1", [source.runtimeId]);
        }
      } finally { await client.end(); }
      this.humanRequestSources = [];
    }
    // Projects last: an issue delete leaves no project reference behind, and
    // dropping the project first would strand the issues in the list.
    for (const id of this.createdProjectIds) {
      try {
        await this.deleteProject(id);
      } catch {
        /* ignore — may already be deleted */
      }
    }
    this.createdProjectIds = [];
  }

  getToken() {
    return this.token;
  }

  getEmail() {
    if (!this.email) {
      throw new Error("Test API client is not logged in");
    }
    return this.email;
  }

  private async authedFetch(path: string, init?: RequestInit) {
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
      ...((init?.headers as Record<string, string>) ?? {}),
    };
    if (this.token) headers["Authorization"] = `Bearer ${this.token}`;
    if (this.workspaceSlug) headers["X-Workspace-Slug"] = this.workspaceSlug;
    else if (this.workspaceId) headers["X-Workspace-ID"] = this.workspaceId;
    return fetch(`${API_BASE}${path}`, { ...init, headers });
  }
}
