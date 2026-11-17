export const WINDOW_CLOSE_CHECK = "window:check-close";
export const WINDOW_CLOSE_REPLY = "window:close-check-result";

export function parseWindowCloseReply(value: unknown): { readonly id: number; readonly allowed: boolean } | null {
  if (typeof value !== "object" || value === null || !("id" in value) || typeof value.id !== "number" || !Number.isSafeInteger(value.id)) return null;
  if (!("allowed" in value) || typeof value.allowed !== "boolean") return null;
  return { id: value.id, allowed: value.allowed };
}
