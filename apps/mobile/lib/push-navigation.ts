import { z } from "zod";

const PushDestinationSchema = z.object({
  recipient_id: z.string().uuid(),
  workspace_slug: z.string().min(1).regex(/^[a-zA-Z0-9_-]+$/),
  type: z.string(),
  inbox_id: z.string().uuid().optional().or(z.literal("")),
  issue_id: z.string().uuid().optional(),
  chat_session_id: z.string().uuid().optional(),
});

export function pushNavigationTarget(data: unknown, userId: string) {
  const result = PushDestinationSchema.safeParse(data);
  if (!result.success || result.data.recipient_id !== userId) return null;
  const { workspace_slug: workspace, inbox_id, issue_id, chat_session_id, type } = result.data;
  if (chat_session_id) {
    return { pathname: "/[workspace]/(tabs)/chat" as const, params: { workspace, session: chat_session_id } };
  }
  if (inbox_id && ["human_action_requested", "autopilot_quota_exceeded", "autopilot_paused"].includes(type)) {
    return { pathname: "/[workspace]/inbox/[id]" as const, params: { workspace, id: inbox_id } };
  }
  if (issue_id) {
    return { pathname: "/[workspace]/issue/[id]" as const, params: { workspace, id: issue_id } };
  }
  return { pathname: "/[workspace]/(tabs)/inbox" as const, params: { workspace } };
}
