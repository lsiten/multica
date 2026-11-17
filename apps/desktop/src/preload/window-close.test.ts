import { afterEach, expect, it, vi } from "vitest";
import { WINDOW_CLOSE_REPLY } from "../shared/window-close";

const mocks = vi.hoisted(() => ({
  check: undefined as ((_event: unknown, id: unknown) => void) | undefined,
  send: vi.fn(),
}));
vi.mock("electron", () => ({ ipcRenderer: {
  on: (_name: string, listener: (_event: unknown, id: unknown) => void) => { mocks.check = listener; },
  send: mocks.send,
} }));
import { installWindowCloseCheck } from "./window-close";

afterEach(() => { vi.clearAllMocks(); });
it("honors existing form unload listeners before answering the main process", () => {
  installWindowCloseCheck();
  const prevent = (event: Event) => event.preventDefault();
  window.addEventListener("beforeunload", prevent);
  try {
    mocks.check?.({}, 1);
    expect(mocks.send).toHaveBeenLastCalledWith(WINDOW_CLOSE_REPLY, { id: 1, allowed: false });
  } finally { window.removeEventListener("beforeunload", prevent); }
  mocks.check?.({}, 2);
  expect(mocks.send).toHaveBeenLastCalledWith(WINDOW_CLOSE_REPLY, { id: 2, allowed: true });
});
it("ignores malformed close requests", () => {
  installWindowCloseCheck();
  mocks.check?.({}, "1");
  expect(mocks.send).not.toHaveBeenCalled();
});
