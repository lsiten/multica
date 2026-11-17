import type { NavigationConfirmation } from "@multica/core/platform";

export function confirmNavigation(request: NavigationConfirmation): boolean {
  const api: unknown = Reflect.get(window, "desktopAPI");
  if (api !== undefined) {
    return typeof api === "object" && api !== null && "confirmNavigation" in api && typeof api.confirmNavigation === "function" && api.confirmNavigation(request) === true;
  }
  return window.confirm(request.message);
}
