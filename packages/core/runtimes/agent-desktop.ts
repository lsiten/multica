import { parseAgentDesktopReadiness } from "../api";
import type { AgentDesktopReadiness } from "../types";

export function cachedAgentDesktopReadiness(
  metadata: Record<string, unknown>,
): AgentDesktopReadiness | null {
  return parseAgentDesktopReadiness(metadata.agent_desktop);
}
