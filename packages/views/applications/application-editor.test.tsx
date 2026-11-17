import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { applicationSchema, defaultApplicationConfig } from "@multica/core/applications";
import en from "../locales/en/applications.json";
import { ApplicationEditor } from "./application-editor";
import { NavigationProvider } from "../navigation";
import { runGuardedNavigation } from "@multica/core/platform";

const mocks = vi.hoisted(() => ({ update: vi.fn() }));
vi.mock("@multica/core/api", () => ({ api: {
  listProjects: async () => ({ projects: [] }),
  updateApplication: mocks.update,
} }));
const id = "11111111-1111-4111-8111-111111111111";
const application = applicationSchema.parse({ id, workspace_id: id, project_id: id, name: "Original", kind: "composition", revision: 1, config: defaultApplicationConfig(), created_at: "2026-11-15T00:00:00Z", updated_at: "2026-11-15T00:00:00Z", created_by: id });
const clients: QueryClient[] = [];

function openEditor(onSaved?: () => void) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  clients.push(client);
  render(<NavigationProvider value={{ push: () => {}, replace: () => {}, back: () => {}, pathname: "/acme/applications", searchParams: new URLSearchParams(), hash: "", getShareableUrl: (path) => new URL(path, "https://app.example").toString() }}><QueryClientProvider client={client}><I18nProvider locale="en" resources={{ en: { applications: en } }}>
    <ApplicationEditor workspaceId={id} application={application} trigger={<button>Open editor</button>} onSaved={onSaved} />
  </I18nProvider></QueryClientProvider></NavigationProvider>);
  fireEvent.click(screen.getByText("Open editor"));
}
afterEach(() => { cleanup(); clients.forEach((client) => client.clear()); clients.length = 0; vi.resetAllMocks(); vi.restoreAllMocks(); });

describe("application editor dismissal", () => {
  it("keeps dirty input after cancelling a discard confirmation", async () => {
    openEditor();
    fireEvent.change(await screen.findByRole("textbox", { name: "Name" }), { target: { value: "Unsaved service" } });

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    const confirmation = await screen.findByRole("alertdialog");
    fireEvent.click(within(confirmation).getByRole("button", { name: "Keep editing" }));

    expect(screen.getByRole("textbox", { name: "Name" })).toHaveValue("Unsaved service");
    expect(mocks.update).not.toHaveBeenCalled();
  });

  it("blocks Escape during saving and retains input when the save fails", async () => {
    let rejectSave: ((error: Error) => void) | undefined;
    mocks.update.mockImplementation(() => new Promise((_resolve, reject) => { rejectSave = reject; }));
    openEditor();
    fireEvent.change(await screen.findByRole("textbox", { name: "Name" }), { target: { value: "Retry me" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(mocks.update).toHaveBeenCalled());

    const navigate = vi.fn();
    runGuardedNavigation(navigate);
    expect(navigate).not.toHaveBeenCalled();

    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    rejectSave?.(new Error("Connection lost"));

    expect(await screen.findByRole("alert")).toHaveTextContent("Connection lost");
    expect(screen.getByRole("textbox", { name: "Name" })).toHaveValue("Retry me");
  });

  it("preserves dirty input when desktop navigation is declined", async () => {
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    const navigate = vi.fn();
    openEditor();
    fireEvent.change(await screen.findByRole("textbox", { name: "Name" }), { target: { value: "Keep draft" } });
    runGuardedNavigation(navigate);
    expect(navigate).not.toHaveBeenCalled();
    expect(confirm).toHaveBeenCalledOnce();
    expect(screen.getByRole("textbox", { name: "Name" })).toHaveValue("Keep draft");
    confirm.mockReturnValue(true);
    runGuardedNavigation(navigate);
    expect(navigate).toHaveBeenCalledOnce();
  });

  it("allows the saved callback to navigate after a successful response", async () => {
    mocks.update.mockResolvedValue({ ...application, name: "Saved service" });
    const navigate = vi.fn();
    openEditor(() => runGuardedNavigation(navigate));
    fireEvent.change(await screen.findByRole("textbox", { name: "Name" }), { target: { value: "Saved service" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(navigate).toHaveBeenCalledOnce());
  });
});
