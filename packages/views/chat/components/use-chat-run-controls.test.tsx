import type { ReactNode } from "react";
import { act, renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useChatRunPolicyStore } from "@multica/core/chat";
import type { ChatAutonomyPolicyOverride, ChatMessage } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enChat from "../../locales/en/chat.json";
import { useChatRunControls } from "./use-chat-run-controls";

type Controller = Parameters<typeof useChatRunControls>[0];
const policy: ChatAutonomyPolicyOverride = {
  mode: "autonomous", max_duration_seconds: 600, max_token_count: 10000,
};

function controller(overrides: Partial<Controller> = {}): Controller {
  return {
    wsId: "workspace-1", activeSessionId: "session-1", activeAgent: { id: "agent-1" },
    messages: [], handleSend: vi.fn(async () => true), ...overrides,
  };
}

function setup(initial: Controller = controller()) {
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={{ en: { chat: enChat } }}>{children}</I18nProvider>
    </QueryClientProvider>
  );
  return renderHook((input: Controller) => ({
    page: useChatRunControls(input),
    floating: useChatRunControls(input),
  }), { wrapper, initialProps: initial });
}

beforeEach(() => {
  useChatRunPolicyStore.setState({ policies: {} });
});

describe("shared chat run controls", () => {
  it.each([true, false])("clears the upcoming-turn policy only when send acceptance is %s", async (accepted) => {
    const { result } = setup(controller({ handleSend: vi.fn(async () => accepted) }));
    act(() => result.current.page.setAutonomyPolicy(policy));
    expect(result.current.floating.autonomyPolicy).toEqual(policy);

    let outcome: boolean | undefined;
    await act(async () => {
      outcome = await result.current.floating.handleSend("Prepare the report");
    });

    expect(outcome).toBe(accepted);
    expect(result.current.page.autonomyPolicy).toEqual(accepted ? null : policy);
  });

  it.each([
    { wsId: "workspace-2" },
    { activeSessionId: "session-2" },
    { activeAgent: { id: "agent-2" } },
  ])("keeps settings isolated when the compose identity changes to %j", (identity) => {
    const initial = controller();
    const { result, rerender } = setup(initial);
    act(() => result.current.page.setAutonomyPolicy(policy));

    rerender({ ...initial, ...identity });
    expect(result.current.page.autonomyPolicy).toBeNull();
    rerender(initial);
    expect(result.current.floating.autonomyPolicy).toEqual(policy);
  });

  it("preserves a newly selected policy when an earlier submission finishes", async () => {
    let accept: (value: boolean) => void = () => undefined;
    const submission = new Promise<boolean>((resolve) => { accept = resolve; });
    const { result } = setup(controller({ handleSend: () => submission }));
    act(() => result.current.page.setAutonomyPolicy(policy));
    let pending: Promise<boolean> | undefined;
    act(() => { pending = result.current.page.handleSend("First report"); });
    const nextPolicy = { ...policy, max_token_count: 20000 };
    act(() => result.current.floating.setAutonomyPolicy(nextPolicy));

    await act(async () => {
      accept(true);
      await pending;
    });

    expect(result.current.page.autonomyPolicy).toEqual(nextPolicy);
  });

  it("retries a failed child using the original input without consuming the next draft policy", async () => {
    const original: ChatMessage = {
      id: "input", chat_session_id: "session-1", role: "user", content: "Prepare the original report",
      task_id: "root-task", created_at: "2026-11-15T00:00:00Z",
    };
    const failed: ChatMessage = {
      id: "failed", chat_session_id: "session-1", role: "assistant", content: "Connection failed",
      task_id: "child-task", input_task_id: "root-task", created_at: "2026-11-15T00:01:00Z",
    };
    const send = vi.fn(async () => true);
    const { result } = setup(controller({ messages: [original, failed], handleSend: send }));
    act(() => result.current.page.setAutonomyPolicy(policy));

    await act(() => result.current.floating.handleFailureAction(failed, "retry"));

    expect(send).toHaveBeenCalledWith("Prepare the original report", undefined);
    expect(result.current.page.autonomyPolicy).toEqual(policy);
  });
});
