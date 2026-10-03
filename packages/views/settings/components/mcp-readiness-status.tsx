import { Badge } from "@multica/ui/components/ui/badge";
import { useT } from "../../i18n";

export function McpReadinessBadge({ state }: { state: string }) {
  const { t } = useT("settings");
  const label = (() => {
    switch (state) {
      case "broker_ready": return t(($) => $.jev.broker_ready);
      case "ready": return t(($) => $.jev.ready);
      case "probing": return t(($) => $.jev.loading);
      case "not_configured": return t(($) => $.jev.not_installed);
      case "provider_unavailable": return t(($) => $.jev.provider_unavailable);
      case "capability_required": return t(($) => $.jev.capability_required);
      case "protocol_error": return t(($) => $.jev.daemon_invalid);
      default: return t(($) => $.jev.daemon_failed);
    }
  })();
  return <Badge variant={state === "ready" || state === "broker_ready" ? "secondary" : "outline"}>{label}</Badge>;
}
