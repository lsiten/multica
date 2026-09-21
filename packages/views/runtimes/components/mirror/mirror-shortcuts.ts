export type MirrorKeyModifier = "shift" | "control" | "alt" | "meta";

/** macOS reserves Command+Tab for application switching; Windows/Linux use Alt+Tab. */
export function appSwitchModifiers(clientOS: string | undefined): readonly MirrorKeyModifier[] {
  const normalized = clientOS?.trim().toLowerCase() ?? "";
  return normalized === "macos" || normalized === "darwin" || normalized.includes("darwin-")
    ? ["meta"]
    : ["alt"];
}
