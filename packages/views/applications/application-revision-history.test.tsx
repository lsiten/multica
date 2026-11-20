import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { applicationBoardSchema, applicationSchema, defaultApplicationConfig } from "@multica/core/applications";
import en from "../locales/en/applications.json";
import { ApplicationRevisionHistory } from "./application-revision-history";

const fixture = vi.hoisted(() => ({ list: vi.fn(), read: vi.fn(), restore: vi.fn() }));
vi.mock("@multica/core/api", () => ({ api: { applicationRevisions: fixture } }));
const id = "11111111-1111-4111-8111-111111111111";
const projectId = "22222222-2222-4222-8222-222222222222";
const revision = (number: number, port: number) => ({ workspace_id: id, application_id: id, revision: number, config: { ...defaultApplicationConfig(), mode: "external" as const, port }, relations: [], actor_type: "member" as const, actor_id: id, created_at: `2026-11-20T00:0${number}:00Z` });
const application = applicationSchema.parse({ id, workspace_id: id, project_id: projectId, name: "Revision fixture", kind: "service", revision: 2, created_by: id, created_at: "2026-11-20T00:00:00Z", updated_at: "2026-11-20T00:02:00Z", config: revision(2, 4200).config });
const board = applicationBoardSchema.parse({ applications: [application], instances: [], endpoints: [], operations: [], summary: { services: 1, compositions: 0, instances: 0, running: 0, unhealthy: 0, offline: 0, stopped: 0, runtimes: 0 } });
const clients: QueryClient[] = [];

function renderHistory() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  clients.push(client);
  render(<QueryClientProvider client={client}><I18nProvider locale="en" resources={{ en: { applications: en } }}><ApplicationRevisionHistory workspaceId={id} application={application} board={board} /></I18nProvider></QueryClientProvider>);
}

afterEach(() => { cleanup(); clients.forEach((client) => client.clear()); clients.length = 0; vi.resetAllMocks(); });

it("compares a historical snapshot and restores it as a new revision", async () => {
  fixture.list.mockResolvedValue({ revisions: [revision(2, 4200), revision(1, 4100)], next_before_revision: null });
  fixture.read.mockResolvedValue(revision(2, 4200));
  fixture.restore.mockResolvedValue({ ...application, revision: 3, config: revision(1, 4100).config });
  renderHistory();

  expect(await screen.findByText("Revision history")).toBeInTheDocument();
  await waitFor(() => expect(fixture.list).toHaveBeenCalled());
  fireEvent.click(screen.getByRole("button", { name: /Revision 1/ }));
  expect(await screen.findByText("port")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Restore revision" }));
  const dialog = await screen.findByRole("alertdialog");
  fireEvent.click(within(dialog).getByRole("button", { name: "Restore revision" }));
  await waitFor(() => expect(fixture.restore).toHaveBeenCalledWith({ workspaceId: id, applicationId: id }, { revision: 2, target_revision: 1 }));
  expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
});
