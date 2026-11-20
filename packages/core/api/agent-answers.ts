import { z } from "zod";
import { parseWithFallback } from "./schema";

export const AgentAnswerPageSchema = z.object({
  task_id: z.string().min(1),
  run_status: z.string().default("unknown"),
  messages: z.array(z.object({
    id: z.string().min(1),
    seq: z.number().int().nonnegative(),
    type: z.enum(["text", "error", "final"]).catch("text"),
    content: z.string(),
    offset: z.number().int().nonnegative().default(0),
    has_more_content: z.boolean().default(false),
    created_at: z.string().default(""),
  })),
  next_cursor: z.string().regex(/^(?:\d+:\d+|final:\d+)$/),
  has_more: z.boolean(),
}).transform((wire) => ({
  taskId: wire.task_id,
  runStatus: wire.run_status,
  messages: wire.messages.map((message) => ({
    id: message.id,
    seq: message.seq,
    type: message.type,
    content: message.content,
    offset: message.offset,
    hasMoreContent: message.has_more_content,
    createdAt: message.created_at,
  })),
  nextCursor: wire.next_cursor,
  hasMore: wire.has_more,
}));

export type AgentAnswerPage = z.infer<typeof AgentAnswerPageSchema>;

export const AgentCollaborationOperationSchema = z.object({
  operation_id: z.string().min(1),
  operation: z.string().min(1),
  status: z.string().min(1),
  source_task_id: z.string().default(""),
  target_agent_id: z.string().default(""),
  target_task_id: z.string().default(""),
  comment_id: z.string().default(""),
  result: z.record(z.string(), z.unknown()).default({}),
  error: z.string().default(""),
}).transform((wire) => ({
  operationId: wire.operation_id,
  operation: wire.operation,
  status: wire.status,
  sourceTaskId: wire.source_task_id,
  targetAgentId: wire.target_agent_id,
  targetTaskId: wire.target_task_id,
  commentId: wire.comment_id,
  result: wire.result,
  error: wire.error,
}));

export type AgentCollaborationOperation = z.infer<typeof AgentCollaborationOperationSchema>;

export function parseAgentAnswerPage(raw: unknown, taskId: string): AgentAnswerPage | null {
  const page = parseWithFallback<AgentAnswerPage | null>(raw, AgentAnswerPageSchema, null, {
    endpoint: "GET /api/tasks/:id/answers", redactPayload: true,
  });
  return page?.taskId === taskId ? page : null;
}

function parseAgentCollaborationOperation(raw: unknown): AgentCollaborationOperation | null {
  return parseWithFallback<AgentCollaborationOperation | null>(raw, AgentCollaborationOperationSchema, null, {
    endpoint: "POST /api/agent-collaboration", redactPayload: true,
  });
}

type CollaborationFetch = (path: string, options?: RequestInit) => Promise<unknown>;

export class AgentCollaborationResponseError extends Error {
  constructor() {
    super("Invalid agent collaboration response");
    this.name = "AgentCollaborationResponseError";
  }
}

export function createAgentCollaborationApi(fetch: CollaborationFetch) {
  const command = async (path: string, body: Record<string, unknown>): Promise<AgentCollaborationOperation> => {
    const raw = await fetch(path, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(body),
    });
    const operation = parseAgentCollaborationOperation(raw);
    if (!operation) throw new AgentCollaborationResponseError();
    return operation;
  };
  return {
    async read(taskId: string, options: { readonly cursor?: string; readonly limit?: number; readonly view?: "final"; readonly signal?: AbortSignal } = {}): Promise<AgentAnswerPage> {
      const query = new URLSearchParams();
      if (options.cursor) query.set("cursor", options.cursor);
      if (options.limit !== undefined) query.set("limit", String(options.limit));
      if (options.view) query.set("view", options.view);
      const raw = await fetch(`/api/tasks/${encodeURIComponent(taskId)}/answers?${query}`, { signal: options.signal });
      const page = parseAgentAnswerPage(raw, taskId);
      if (!page) throw new AgentCollaborationResponseError();
      return page;
    },
    delegate(issueId: string, input: { targetAgentId: string; content: string; clientRequestId: string }) {
      return command(`/api/issues/${encodeURIComponent(issueId)}/collaboration/delegate`, {
        target_agent_id: input.targetAgentId, content: input.content, client_request_id: input.clientRequestId,
      });
    },
    append(taskId: string, input: { content: string; clientRequestId: string }) {
      return command(`/api/tasks/${encodeURIComponent(taskId)}/collaboration/append`, input);
    },
    next(taskId: string, input: { content: string; clientRequestId: string }) {
      return command(`/api/tasks/${encodeURIComponent(taskId)}/collaboration/next`, input);
    },
    watch(taskId: string, clientRequestId: string) {
      return command(`/api/tasks/${encodeURIComponent(taskId)}/collaboration/watch`, { client_request_id: clientRequestId });
    },
    stop(taskId: string, clientRequestId: string) {
      return command(`/api/tasks/${encodeURIComponent(taskId)}/collaboration/stop`, { client_request_id: clientRequestId });
    },
    async operation(operationId: string, options: { readonly signal?: AbortSignal } = {}): Promise<AgentCollaborationOperation> {
      const raw = await fetch(`/api/agent-collaboration/operations/${encodeURIComponent(operationId)}`, { signal: options.signal });
      const operation = parseAgentCollaborationOperation(raw);
      if (!operation) throw new AgentCollaborationResponseError();
      return operation;
    },
  };
}
