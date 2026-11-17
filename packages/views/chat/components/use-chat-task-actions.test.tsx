import type { ReactNode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "@multica/core/api";
import { useChatTaskStopping } from "@multica/core/chat";
import { chatKeys } from "@multica/core/chat/queries";
import type { CancelTaskResponse, ChatPendingTask } from "@multica/core/types";
import { useChatTaskActions } from "./use-chat-task-actions";

vi.mock("@multica/core/api", async (importOriginal) => ({
  ...await importOriginal<typeof import("@multica/core/api")>(),
  api: { cancelTaskById: vi.fn() },
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("../../i18n", () => ({ useT: () => ({ t: () => "Operation failed" }) }));
vi.mock("@multica/core/logger", async (importOriginal) => ({
  ...await importOriginal<typeof import("@multica/core/logger")>(),
  createLogger: () => ({ info: vi.fn(), warn: vi.fn(), error: vi.fn() }),
}));

const receipt: CancelTaskResponse = {
  id: "task-1", agent_id: "agent-1", runtime_id: "runtime-1", issue_id: "",
  status: "cancelled", priority: 0, dispatched_at: null, started_at: null,
  completed_at: null, result: null, error: null, created_at: "2026-11-15T00:00:00Z",
};
const pending: ChatPendingTask = {
  task_id: "task-1", status: "running", supports_queue: true,
  queued_tasks: [{ task_id: "task-2", status: "queued", created_at: "2026-11-15T00:01:00Z" }],
};

function setup() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  client.setQueryData(chatKeys.pendingTask("session-1"), pending);
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  const restore = vi.fn();
  return { client, ...renderHook(() => ({
    actions: useChatTaskActions("session-1", restore),
    stopping: useChatTaskStopping("ws-1", "session-1", "task-1"),
  }), { wrapper }) };
}

beforeEach(() => vi.clearAllMocks());

describe("confirmed chat cancellation", () => {
  it("keeps the running task and its queue visible until the server acknowledges stopping", async () => {
    const response = Promise.withResolvers<CancelTaskResponse>();
    vi.mocked(api.cancelTaskById).mockReturnValue(response.promise);
    const { client, result } = setup();
    let cancellation: Promise<CancelTaskResponse | null> | undefined;

    act(() => { cancellation = result.current.actions.cancelChatTask("task-1", "session-1", { restoreDraftToInput: true, source: "active-input" }); });
    await waitFor(() => expect(result.current.stopping).toBe(true));
    expect(client.getQueryData(chatKeys.pendingTask("session-1"))).toEqual(pending);
    await act(async () => { response.resolve(receipt); await cancellation; });

    await waitFor(() => expect(result.current.stopping).toBe(false));
    expect(client.getQueryData<ChatPendingTask>(chatKeys.pendingTask("session-1"))?.task_id).toBe("task-2");
  });

  it.each(["request failed", "unacknowledged task"])("preserves the authoritative pending task after %s", async (failure) => {
    if (failure === "request failed") vi.mocked(api.cancelTaskById).mockRejectedValue(new Error("Request failed"));
    else vi.mocked(api.cancelTaskById).mockResolvedValue({ ...receipt, id: "" });
    const { client, result } = setup();
    let outcome: CancelTaskResponse | null | undefined;

    await act(async () => { outcome = await result.current.actions.cancelChatTask("task-1", "session-1", { restoreDraftToInput: true, source: "active-input" }); });

    expect(outcome).toBeNull();
    expect(client.getQueryData(chatKeys.pendingTask("session-1"))).toEqual(pending);
    expect(result.current.stopping).toBe(false);
  });
});
