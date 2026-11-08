import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { applicationSchema, applicationBoardSchema, applicationInstanceSchema, applicationOperationSchema, defaultApplicationConfig, type ApplicationOperation, type ApplicationOperationRequest } from "@multica/core/applications";
import en from "../locales/en/applications.json";
import agents from "../locales/en/agents.json";
import common from "../locales/en/common.json";
import { ApplicationOperationDialog } from "./application-operation-dialog";
import { ApplicationDetail } from "./application-detail";

const fixture = vi.hoisted(() => ({
  enqueue: vi.fn<(ws: string, id: string, input: ApplicationOperationRequest) => Promise<ApplicationOperation>>(),
  safeId: vi.fn(),
  getApplication: vi.fn(), getBoard: vi.fn(), getPlan: vi.fn(),
  listRuntimes: vi.fn(async () => []), listMembers: vi.fn(async () => []),
  supportsLocal: vi.fn(async () => true),
}));
vi.mock("@multica/core/api", () => ({ api: {
  enqueueApplicationOperation: fixture.enqueue, getApplication: fixture.getApplication,
  getApplicationBoard: fixture.getBoard, previewApplicationPlan: fixture.getPlan,
  listRuntimes: fixture.listRuntimes, listMembers: fixture.listMembers,
  supportsLocalApplication: fixture.supportsLocal,
} }));
vi.mock("@multica/core/utils", async (original) => ({ ...await original<typeof import("@multica/core/utils")>(), createSafeId: fixture.safeId }));
vi.mock("@multica/core/auth", () => ({
  useAuthStore: Object.assign(<T,>(selector: (state: { user: null }) => T) => selector({ user: null }), { getState: () => ({ user: null }) }),
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace-fixture" }));
vi.mock("@multica/core/paths", () => ({ useWorkspacePaths: () => ({ applications: () => "/applications", applicationDetail: (id: string) => `/applications/${id}` }) }));
vi.mock("../navigation", () => ({ AppLink: ({ children }: { children: React.ReactNode }) => <span>{children}</span>, useNavigation: () => ({ push: vi.fn() }) }));
vi.mock("./application-editor", () => ({ ApplicationEditor: () => null }));

const id = "11111111-1111-4111-8111-111111111111";
const runtimeId = "22222222-2222-4222-8222-222222222222";
const instanceId = "33333333-3333-4333-8333-333333333333";
const operationId = "44444444-4444-4444-8444-444444444444";
const application = applicationSchema.parse({ id, workspace_id: id, project_id: id, created_by: id, name: "Fixture service", kind: "service", revision: 1, created_at: "2026-11-08T00:00:00Z", updated_at: "2026-11-08T00:00:00Z", config: defaultApplicationConfig() });
const plan = { root_id: id, nodes: [{ id, revision: 1, required: true, dependencies: [] }], waves: [[id]] };
const instance = applicationInstanceSchema.parse({ id: instanceId, workspace_id: id, application_id: id, runtime_id: runtimeId, revision: 1, generation: 1, desired_state: "running", process_state: "running", health_state: "healthy", runtime_state: "online", status: "running", can_manage: true });
const operation = applicationOperationSchema.parse({ id: operationId, workspace_id: id, application_id: id, action: "start", actor_type: "member", actor_id: id, state: "queued", created_at: "2026-11-08T00:00:00Z", snapshot: { root_runtime_id: runtimeId, root_revision: 1, plan, placements: {} } });
const board = applicationBoardSchema.parse({ applications: [application], instances: [instance], endpoints: [], operations: [], summary: { services: 1, compositions: 0, instances: 1, running: 1, unhealthy: 0, offline: 0, stopped: 0, runtimes: 1 } });

function renderView(child: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } } });
  return render(<QueryClientProvider client={client}><I18nProvider locale="en" resources={{ en: { applications: en, agents, common } }}>{child}</I18nProvider></QueryClientProvider>);
}
function submitted(index: number) {
  const call = fixture.enqueue.mock.calls[index];
  if (!call) throw new Error(`Missing submitted operation ${index}`);
  return call[2];
}

async function submitDialog(name: string, role = "dialog") {
  const dialog = await screen.findByRole(role);
  const button = within(dialog).getByRole("button", { name });
  await waitFor(() => expect(button).toBeEnabled());
  fireEvent.click(button);
}

beforeEach(() => {
  fixture.enqueue.mockReset().mockRejectedValue(new Error("Connection lost"));
  fixture.safeId.mockReset().mockReturnValue("55555555-5555-4555-8555-555555555555");
  fixture.getApplication.mockResolvedValue(application);
  fixture.getBoard.mockResolvedValue(board);
  fixture.getPlan.mockResolvedValue(plan);
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe("application operation confirmation", () => {
  it("uses the secure ID helper and keeps the same request key after a failed submission", async () => {
    renderView(<ApplicationOperationDialog workspaceId={id} application={application} board={board} action="start" initialRuntimeId={runtimeId} trigger={<button>Open start</button>} />);
    fireEvent.click(screen.getByText("Open start"));
    await submitDialog("Start");
    await screen.findByText("Connection lost");
    await submitDialog("Start");
    await waitFor(() => expect(fixture.enqueue).toHaveBeenCalledTimes(2));
    expect(submitted(0).idempotency_key).toBe("55555555-5555-4555-8555-555555555555");
    expect(submitted(1)).toEqual(submitted(0));
  });

  it("shows ID-generation failure without submitting an operation", async () => {
    fixture.safeId.mockImplementation(() => { throw new Error("Secure ID unavailable"); });
    renderView(<ApplicationOperationDialog workspaceId={id} application={application} board={board} action="start" initialRuntimeId={runtimeId} trigger={<button>Open start</button>} />);
    fireEvent.click(screen.getByText("Open start"));
    await submitDialog("Start");
    expect(await screen.findByText("Secure ID unavailable")).toBeInTheDocument();
    expect(fixture.enqueue).not.toHaveBeenCalled();
  });

  it("reuses the delete-instance key until the confirmation is closed", async () => {
    renderView(<ApplicationDetail applicationId={id} />);
    fireEvent.click(await screen.findByRole("button", { name: "Delete instance" }));
    await submitDialog("Delete instance", "alertdialog");
    await screen.findByText("Connection lost");
    await submitDialog("Delete instance", "alertdialog");
    await waitFor(() => expect(fixture.enqueue).toHaveBeenCalledTimes(2));
    expect(submitted(1).idempotency_key).toBe(submitted(0).idempotency_key);
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    fireEvent.click(within(screen.getByRole("alertdialog")).getByText("Cancel"));
    fixture.safeId.mockReturnValue("66666666-6666-4666-8666-666666666666");
    fireEvent.click(await screen.findByRole("button", { name: "Delete instance" }));
    await submitDialog("Delete instance", "alertdialog");
    await waitFor(() => expect(fixture.enqueue).toHaveBeenCalledTimes(3));
    expect(submitted(2).idempotency_key).not.toBe(submitted(0).idempotency_key);
  });

  it.each(["partial", "failed"])("expands %s operations with blocked steps and offers review without replay", async (state) => {
    const incomplete = applicationOperationSchema.parse({ ...operation, state, steps: [{ id: operationId, instance_id: instanceId, application_id: id, runtime_id: runtimeId, generation: 1, wave: 0, required: true, action: "start", state: "blocked", error: "Database is unavailable" }] });
    fixture.getBoard.mockResolvedValue({ ...board, operations: [incomplete] });
    fixture.getApplication.mockResolvedValue({ ...application, revision: 2 });
    renderView(<ApplicationDetail applicationId={id} />);
    fireEvent.click(await screen.findByRole("button", { name: "Activity" }));
    const error = await screen.findByText("Database is unavailable");
    expect(error.closest("details")).toHaveAttribute("open");
    fireEvent.click(screen.getByRole("button", { name: "Review plan" }));
    expect(await screen.findByText("Startup plan")).toBeInTheDocument();
    expect(fixture.enqueue).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Activity" }));
    fireEvent.click(screen.getByRole("button", { name: "Review operation" }));
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(fixture.enqueue).not.toHaveBeenCalled();
    await submitDialog("Start");
    await waitFor(() => expect(fixture.enqueue).toHaveBeenCalledOnce());
    expect(submitted(0)).toMatchObject({ revision: 2, runtime_id: runtimeId });
  });
});
