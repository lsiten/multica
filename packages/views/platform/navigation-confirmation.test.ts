import { afterEach, expect, it, vi } from "vitest";
import { confirmNavigation } from "./navigation-confirmation";

const original = Object.getOwnPropertyDescriptor(window, "desktopAPI");
const request = { message: "Discard changes?", cancelLabel: "Keep editing", confirmLabel: "Discard" };
afterEach(() => {
  if (original) Object.defineProperty(window, "desktopAPI", original);
  else Reflect.deleteProperty(window, "desktopAPI");
  vi.restoreAllMocks();
});
it("uses browser confirmation on web", () => {
  Reflect.deleteProperty(window, "desktopAPI");
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
  expect(confirmNavigation(request)).toBe(true);
  expect(confirm).toHaveBeenCalledWith(request.message);
});
it("uses the synchronous native bridge instead of a suppressed unload dialog", () => {
  const confirm = vi.fn(() => true);
  Object.defineProperty(window, "desktopAPI", { configurable: true, value: { confirmNavigation: confirm } });
  const browser = vi.spyOn(window, "confirm");
  expect(confirmNavigation(request)).toBe(true);
  expect(confirm).toHaveBeenCalledWith(request);
  expect(browser).not.toHaveBeenCalled();
});
it("never treats a malformed native reply as approval", () => {
  Object.defineProperty(window, "desktopAPI", { configurable: true, value: { confirmNavigation: () => "true" } });
  expect(confirmNavigation(request)).toBe(false);
});
