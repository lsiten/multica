// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
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
  delete (window as unknown as { daemonAPI?: unknown }).daemonAPI;
});

describe("McpReadinessCard", () => {
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
