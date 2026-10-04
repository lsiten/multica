// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, render, screen } from "@testing-library/react";
import settings from "../../locales/en/settings.json";
import { I18nProvider } from "@multica/core/i18n/react";
import { McpReadinessCard } from "./mcp-readiness-card";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
vi.mock("@multica/core/paths",()=>({useCurrentWorkspace:()=>({id:"ws"})}));

function renderCard() {
  return render(
    <QueryClientProvider client={new QueryClient()}><I18nProvider locale="en" resources={{ en: { settings } }}>
      <McpReadinessCard />
    </I18nProvider></QueryClientProvider>,
  );
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
  delete (window as unknown as { daemonAPI?: unknown }).daemonAPI;
});

describe("McpReadinessCard", () => {
  it("updates the connection count after runs exit while retaining the broker", async () => {
    vi.useFakeTimers();
    const broker = { name: "multica-llm2jev", instance_id: "builtin-broker", enabled: true, scope: "daemon", ready: true, state: "broker_ready" };
    const task = { ...broker, instance_id: "mcp-1", scope: "task", state: "ready" };
    const readiness = vi.fn().mockResolvedValue([task, broker]);
    Object.defineProperty(window, "daemonAPI", { configurable: true, value: { getMcpReadiness: readiness } });
    await act(async () => { renderCard(); });
    expect(screen.getByText("Active run connections: 1")).toBeInTheDocument();
    readiness.mockResolvedValue([broker]);
    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    expect(screen.queryByText("Active run connections: 1")).not.toBeInTheDocument();
    expect(screen.getAllByText("multica-llm2jev")).toHaveLength(1);
    expect(screen.getByText("Daemon broker online")).toBeInTheDocument();
  });
  it("groups built-in task connections without hiding failures or collapsing custom servers", async () => {
    const task = { enabled: true, scope: "task", ready: true, state: "ready" };
    Object.defineProperty(window, "daemonAPI", {
      configurable: true,
      value: { getMcpReadiness: vi.fn().mockResolvedValue([
        { ...task, name: "multica-llm2jev", instance_id: "builtin-broker", scope: "daemon", state: "broker_ready" },
        { ...task, name: "multica-llm2jev", instance_id: "mcp-1", workspace_id: "ws" },
        { ...task, name: "multica-llm2jev", instance_id: "mcp-2", workspace_id: "ws", state: "timeout", ready: false },
        { ...task, name: "multica-llm2jev", instance_id: "mcp-3", workspace_id: "ws", state: "probing", ready: false },
        { ...task, name: "custom-server", instance_id: "mcp-4" },
        { ...task, name: "custom-server", instance_id: "mcp-5" },
      ]) },
    });
    renderCard();
    await screen.findByText("Daemon broker online");
    expect(screen.getAllByText("multica-llm2jev")).toHaveLength(1);
    expect(screen.getByText("Active run connections: 3")).toBeInTheDocument();
    expect(screen.getByText("Failed connections: 1")).toBeInTheDocument();
    expect(screen.getByText("Pending checks: 1")).toBeInTheDocument();
    expect(screen.getAllByText("custom-server")).toHaveLength(2);
  });

  it("groups legacy task entries while preserving unavailable connections", async () => {
    Object.defineProperty(window, "daemonAPI", {
      configurable: true,
      value: { getMcpReadiness: vi.fn().mockResolvedValue([
        { name: "multica-identity-actions", instance_id: "mcp-1", enabled: true, scope: "task", ready: true, state: "ready" },
        { name: "multica-identity-actions", instance_id: "mcp-2", enabled: true, scope: "task", ready: false, state: "provider_unavailable" },
      ]) },
    });
    renderCard();
    await screen.findByText("Active run connections: 2");
    expect(screen.getAllByText("multica-identity-actions")).toHaveLength(1);
    expect(screen.getByText("Failed connections: 1")).toBeInTheDocument();
  });

  it("renders a daemon broker as online instead of not installed", async () => {
    Object.defineProperty(window, "daemonAPI", {
      configurable: true,
      value: {
        getMcpReadiness: vi.fn().mockResolvedValue([
          {
            name: "multica-llm2jev",
            enabled: true,
            scope: "daemon",
            ready: true,
            state: "broker_ready",
            reason: "broker_listening",
          },
        ]),
      },
    });
    renderCard();
    expect(await screen.findByText("Daemon broker online")).toBeInTheDocument();
    expect(screen.queryByText("Not installed")).not.toBeInTheDocument();
  });

  it("keeps task-not-started explicit for legacy daemons", async () => {
    Object.defineProperty(window, "daemonAPI", {
      configurable: true,
      value: {
        getMcpReadiness: vi.fn().mockResolvedValue([
          {
            name: "multica-llm2jev",
            enabled: true,
            scope: "task",
            ready: false,
            state: "not_configured",
            reason: "task_not_started",
          },
        ]),
      },
    });
    renderCard();
    expect(await screen.findByText("Task-scoped · starts automatically with the next task")).toBeInTheDocument();
  });
});
