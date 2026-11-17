import type { NavigationConfirmation } from "@multica/core/platform";

export const NAVIGATION_CONFIRM_CHANNEL = "navigation:confirm";

export function parseNavigationConfirmation(value: unknown): NavigationConfirmation | null {
  if (typeof value !== "object" || value === null) return null;
  if (!("message" in value) || typeof value.message !== "string" || !value.message.trim() || value.message.length > 4096) return null;
  if (!("cancelLabel" in value) || typeof value.cancelLabel !== "string" || !value.cancelLabel.trim() || value.cancelLabel.length > 128) return null;
  if (!("confirmLabel" in value) || typeof value.confirmLabel !== "string" || !value.confirmLabel.trim() || value.confirmLabel.length > 128) return null;
  return { message: value.message, cancelLabel: value.cancelLabel, confirmLabel: value.confirmLabel };
}
