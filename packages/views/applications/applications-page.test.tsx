import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { WorkspaceSlugProvider } from "@multica/core/paths";
import { applicationBoardSchema, applicationSchema, applicationInstanceSchema, defaultApplicationConfig } from "@multica/core/applications";
import { NavigationProvider } from "../navigation";
import en from "../locales/en/applications.json";
import layout from "../locales/en/layout.json";
import { ApplicationsPage } from "./applications-page";

const mocks = vi.hoisted(() => ({ board: vi.fn() }));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "11111111-1111-4111-8111-111111111111" }));
vi.mock("@multica/core/api", () => ({ api: {
  getApplicationBoard: mocks.board,
  listProjects: async () => ({ projects: [{ id: "11111111-1111-4111-8111-111111111111", workspace_id: "11111111-1111-4111-8111-111111111111", title: "Customer portal" }] }),
  listRuntimes: async () => [],
} }));
const id = "11111111-1111-4111-8111-111111111111";
const app = applicationSchema.parse({ id, workspace_id: id, project_id: id, name: "Customer API", kind: "service", revision: 1, created_by: id, config: defaultApplicationConfig(), created_at: "", updated_at: "" });
const instance = applicationInstanceSchema.parse({ id, workspace_id: id, application_id: id, runtime_id: id, revision: 1, generation: 1, desired_state: "running", process_state: "running", health_state: "unhealthy", runtime_state: "online", status: "unhealthy" });
const board = applicationBoardSchema.parse({ applications: [app], instances: [instance], operations: [], endpoints: [], summary: { services: 1, compositions: 0, instances: 1, running: 0, unhealthy: 1, offline: 0, stopped: 0, runtimes: 1 } });
const clients: QueryClient[] = [];

function PageHarness() {
  const [query, setQuery] = useState("");
  const client = useState(() => {
    const value = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    clients.push(value);
    return value;
  })[0];
  return <WorkspaceSlugProvider slug="acme"><NavigationProvider value={{ pathname: "/acme/applications", searchParams: new URLSearchParams(query), hash: "", push: () => {}, replace: (url) => setQuery(new URL(url, "https://app.example").search), back: () => {}, getShareableUrl: (path) => new URL(path, "https://app.example").toString() }}><QueryClientProvider client={client}><I18nProvider locale="en" resources={{ en: { applications: en, layout } }}><ApplicationsPage /></I18nProvider></QueryClientProvider></NavigationProvider></WorkspaceSlugProvider>;
}

afterEach(() => { cleanup(); clients.forEach((client) => client.clear()); clients.length = 0; vi.resetAllMocks(); });

it("opens the attention board from its summary and separates unhealthy instances from process failures", async () => {
  mocks.board.mockResolvedValue(board);
  render(<PageHarness />);
  fireEvent.click(await screen.findByRole("button", { name: /Needs attention/ }));
  expect(await screen.findByRole("button", { name: "Run board", pressed: true })).toBeInTheDocument();
  const serviceLink = await screen.findByRole("link", { name: /Customer API/ });
  expect(serviceLink.closest("section")).toHaveTextContent("Unhealthy");
  expect(serviceLink.closest("section")).not.toHaveTextContent("Failed");
});

it("clears a search with no matches and restores the existing applications", async () => {
  mocks.board.mockResolvedValue(board);
  render(<PageHarness />);
  await screen.findByRole("link", { name: /Customer API/ });
  fireEvent.change(screen.getByRole("textbox", { name: "Search applications" }), { target: { value: "missing service" } });
  expect(await screen.findByText("No matching applications")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Clear search" }));
  expect(await screen.findByRole("link", { name: /Customer API/ })).toBeInTheDocument();
  expect(screen.getByRole("textbox", { name: "Search applications" })).toHaveValue("");
});
