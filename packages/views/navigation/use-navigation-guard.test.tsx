import { afterEach, expect, it, vi } from "vitest";
import { cleanup, renderHook } from "@testing-library/react";
import { useNavigationGuard } from "./context";

const desktop = vi.hoisted(() => ({ value: false }));
vi.mock("../platform/local-directory", () => ({ isDesktopShell: () => desktop.value }));
afterEach(() => { cleanup(); desktop.value = false; });

it.each([
  { platform: "web", desktop: false, accepts: true, prevent: true },
  { platform: "desktop", desktop: true, accepts: true, prevent: false },
  { platform: "desktop", desktop: true, accepts: false, prevent: true },
])("handles $platform native unload when accepts=$accepts", (scenario) => {
  desktop.value = scenario.desktop;
  const guard = vi.fn(() => scenario.accepts);
  renderHook(() => useNavigationGuard(guard, true));
  const event = new Event("beforeunload", { cancelable: true });
  window.dispatchEvent(event);
  expect(event.defaultPrevented).toBe(scenario.prevent);
  expect(guard).toHaveBeenCalledTimes(scenario.desktop ? 1 : 0);
});

it("removes native unload protection when editing finishes or the view unmounts", () => {
  const view = renderHook(({ dirty }) => useNavigationGuard(() => false, dirty), { initialProps: { dirty: true } });
  view.rerender({ dirty: false });
  let event = new Event("beforeunload", { cancelable: true });
  window.dispatchEvent(event);
  expect(event.defaultPrevented).toBe(false);
  view.rerender({ dirty: true });
  view.unmount();
  event = new Event("beforeunload", { cancelable: true });
  window.dispatchEvent(event);
  expect(event.defaultPrevented).toBe(false);
});

it("cancels a real desktop unload without blocking inside another native dialog", () => {
  desktop.value = true;
  const add = vi.spyOn(window, "addEventListener");
  const guard = vi.fn(() => true);
  try {
    renderHook(() => useNavigationGuard(guard, true));
    const listener = add.mock.calls.find(([name]) => name === "beforeunload")?.[1];
    expect(listener).toBeTypeOf("function");
    if (typeof listener !== "function") return;
    const event = { isTrusted: true, preventDefault: vi.fn(), returnValue: "" };
    Reflect.apply(listener, window, [event]);
    expect(event.preventDefault).toHaveBeenCalledOnce();
    expect(guard).not.toHaveBeenCalled();
  } finally { add.mockRestore(); }
});
