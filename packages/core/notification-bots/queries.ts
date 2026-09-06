import {
  queryOptions,
  useMutation,
  useQueryClient,
} from "@tanstack/react-query";
import { api } from "../api";
import type { SaveNotificationBot } from "./schema";

export const notificationBotKeys = {
  all: (workspaceId: string) => ["notification-bots", workspaceId] as const,
};
export function notificationBotOptions(
  workspaceId: string,
  workspaceSlug: string,
) {
  return queryOptions({
    queryKey: notificationBotKeys.all(workspaceId),
    queryFn: () => api.listNotificationBots(workspaceSlug),
  });
}

type BotAction =
  | { readonly kind: "save"; readonly input: SaveNotificationBot }
  | { readonly kind: "delete"; readonly id: string }
  | { readonly kind: "test"; readonly id: string };

export function useNotificationBotAction(
  workspaceId: string,
  workspaceSlug: string,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (action: BotAction) => {
      switch (action.kind) {
        case "save":
          return api.saveNotificationBot(action.input, workspaceSlug);
        case "delete":
          return api.deleteNotificationBot(action.id, workspaceSlug);
        case "test":
          return api.testNotificationBot(action.id, workspaceSlug);
        default: {
          const exhaustive: never = action;
          return exhaustive;
        }
      }
    },
    onSettled: () =>
      queryClient.invalidateQueries({
        queryKey: notificationBotKeys.all(workspaceId),
      }),
  });
}
