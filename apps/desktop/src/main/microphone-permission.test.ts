// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import type { Session } from "electron";
import { requestMicrophoneAccess } from "./microphone-permission";

const mocks = vi.hoisted(() => ({
  register: vi.fn<Session["setPermissionRequestHandler"]>(),
  status: vi.fn(() => "not-determined"),
  request: vi.fn(async () => true),
}));
vi.mock("electron", () => ({
  session: { defaultSession: { setPermissionRequestHandler: mocks.register } },
  systemPreferences: { getMediaAccessStatus: mocks.status, askForMediaAccess: mocks.request },
}));
afterEach(() => { vi.clearAllMocks(); vi.unstubAllGlobals(); });

it.each([
  ["not-determined", true, true],
  ["not-determined", false, true],
  ["granted", true, false],
  ["denied", false, false],
  ["restricted", false, false],
  ["unknown", false, false],
])("handles microphone status %s with result %s", async (status, allowed, prompt) => {
  vi.stubGlobal("process", { ...process, platform: "darwin" });
  mocks.status.mockReturnValue(status);
  mocks.request.mockResolvedValue(allowed);
  await expect(requestMicrophoneAccess()).resolves.toBe(allowed);
  expect(mocks.request).toHaveBeenCalledTimes(prompt ? 1 : 0);
});
