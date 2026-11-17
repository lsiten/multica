// @vitest-environment node
import { beforeEach, expect, it, vi } from "vitest";

interface ConfirmationEvent {
  returnValue: unknown;
  readonly sender: { readonly mainFrame: object };
  readonly senderFrame: object;
}
const mocks = vi.hoisted(() => ({
  listener: undefined as ((event: ConfirmationEvent, value: unknown) => void) | undefined,
  window: {},
  fromWebContents: vi.fn(),
  showMessageBoxSync: vi.fn(),
}));
vi.mock("electron", () => ({
  BrowserWindow: { fromWebContents: mocks.fromWebContents },
  dialog: { showMessageBoxSync: mocks.showMessageBoxSync },
  ipcMain: { on: (_channel: string, listener: (event: ConfirmationEvent, input: unknown) => void) => { mocks.listener = listener; } },
}));
import { installNavigationConfirmation } from "./navigation-confirmation";

const request = { message: "Discard the unsaved workflow?", cancelLabel: "Keep editing", confirmLabel: "Discard" };
beforeEach(() => {
  vi.clearAllMocks();
  mocks.fromWebContents.mockReturnValue(mocks.window);
  installNavigationConfirmation();
});
function event(): ConfirmationEvent {
  const frame = {};
  return { returnValue: undefined, sender: { mainFrame: frame }, senderFrame: frame };
}
it.each([0, 1])("returns explicit native choice %s with cancellation as the default", (choice) => {
  mocks.showMessageBoxSync.mockReturnValue(choice);
  const input = event();
  mocks.listener?.(input, request);
  expect(input.returnValue).toBe(choice === 1);
  expect(mocks.showMessageBoxSync).toHaveBeenCalledWith(mocks.window, expect.objectContaining({
    message: request.message, buttons: [request.cancelLabel, request.confirmLabel], defaultId: 0, cancelId: 0,
  }));
});
it("rejects subframe prompts and malformed requests without opening a dialog", () => {
  const subframe = { ...event(), senderFrame: {} };
  mocks.listener?.(subframe, request);
  expect(subframe.returnValue).toBe(false);
  const invalid = event();
  mocks.listener?.(invalid, { message: 42 });
  expect(invalid.returnValue).toBe(false);
  expect(mocks.showMessageBoxSync).not.toHaveBeenCalled();
});
