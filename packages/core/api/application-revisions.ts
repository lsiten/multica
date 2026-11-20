import { applicationRevisionPageSchema, applicationRevisionSchema, type ApplicationRevision, type ApplicationRevisionPage, type ApplicationRevisionScope, type RestoreApplicationRevisionRequest } from "../applications/revisions";
import { applicationSchema, type Application } from "../applications/schema";
import { parseWithFallback } from "./schema";

type RevisionFetch = (path: string, options?: RequestInit) => Promise<unknown>;

export class ApplicationRevisionResponseError extends Error {
  constructor(readonly source: "history" | "revision" | "restore") {
    super(`Invalid application ${source} response`);
    this.name = "ApplicationRevisionResponseError";
  }
}

function validPage(page: ApplicationRevisionPage, scope: ApplicationRevisionScope, beforeRevision: number | undefined) {
  let previous = beforeRevision ?? Infinity;
  for (const revision of page.revisions) {
    if (revision.workspace_id !== scope.workspaceId || revision.application_id !== scope.applicationId || revision.revision >= previous) return false;
    previous = revision.revision;
  }
  return page.next_before_revision === null || page.next_before_revision === page.revisions.at(-1)?.revision;
}

export function createApplicationRevisionApi(fetch: RevisionFetch) {
  const headers = (scope: ApplicationRevisionScope) => ({ "X-Workspace-ID": scope.workspaceId, "X-Workspace-Slug": "" });
  const base = (scope: ApplicationRevisionScope) => `/api/applications/${encodeURIComponent(scope.applicationId)}`;
  return {
    async list(scope: ApplicationRevisionScope, options: { readonly beforeRevision?: number; readonly signal?: AbortSignal } = {}) {
      const query = new URLSearchParams();
      if (options.beforeRevision !== undefined) query.set("before_revision", String(options.beforeRevision));
      const raw = await fetch(`${base(scope)}/revisions?${query}`, { signal: options.signal, headers: headers(scope) });
      const page = parseWithFallback<ApplicationRevisionPage | null>(raw, applicationRevisionPageSchema, null, { endpoint: "GET /api/applications/:id/revisions", redactPayload: true });
      if (!page || !validPage(page, scope, options.beforeRevision)) throw new ApplicationRevisionResponseError("history");
      return page;
    },
    async read(scope: ApplicationRevisionScope, revision: number, signal?: AbortSignal) {
      const raw = await fetch(`${base(scope)}/revisions/${revision}`, { signal, headers: headers(scope) });
      const result = parseWithFallback<ApplicationRevision | null>(raw, applicationRevisionSchema, null, { endpoint: "GET /api/applications/:id/revisions/:revision", redactPayload: true });
      if (!result || result.workspace_id !== scope.workspaceId || result.application_id !== scope.applicationId || result.revision !== revision) throw new ApplicationRevisionResponseError("revision");
      return result;
    },
    async restore(scope: ApplicationRevisionScope, input: RestoreApplicationRevisionRequest) {
      const raw = await fetch(`${base(scope)}/restore`, { method: "POST", body: JSON.stringify(input), headers: headers(scope) });
      const result = parseWithFallback<Application | null>(raw, applicationSchema, null, { endpoint: "POST /api/applications/:id/restore", redactPayload: true });
      if (!result || result.workspace_id !== scope.workspaceId || result.id !== scope.applicationId || result.revision !== input.revision + 1) throw new ApplicationRevisionResponseError("restore");
      return result;
    },
  };
}
