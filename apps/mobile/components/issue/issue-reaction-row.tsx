/**
 * Issue-level reaction row. Sits right under the description, mirroring
 * web's `issue-detail.tsx:785` placement.
 *
 * Reads issue.reactions from the detail cache passed by the parent. No
 * separate query — single source of truth on the detail object.
 *
 * The add action remains visible before the first reaction is created.
 */
import { useCallback, useMemo } from "react";
import { View } from "react-native";
import { router } from "expo-router";
import type { Issue } from "@multica/core/types";
import { ReactionBar } from "./reaction-bar";
import { useToggleIssueReaction } from "@/data/mutations/issues";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { Button } from "@/components/ui/button";
import { Text } from "@/components/ui/text";
import { useT } from "@/lib/i18n";

export function IssueReactionRow({ issue }: { issue: Issue }) {
  const userId = useAuthStore((s) => s.user?.id);
  const workspace = useWorkspaceStore((state) => state.currentWorkspaceSlug);
  const { t } = useT("issues");
  const reactions = useMemo(
    () => issue.reactions ?? [],
    [issue.reactions],
  );
  const toggle = useToggleIssueReaction(issue.id);

  const onToggle = useCallback(
    (emoji: string) => {
      const existing = reactions.find(
        (r) =>
          r.emoji === emoji &&
          r.actor_type === "member" &&
          r.actor_id === userId,
      );
      toggle.mutate({ emoji, existing });
    },
    [reactions, userId, toggle],
  );

  return (
    <View className="px-4 pb-3">
      <ReactionBar
        reactions={reactions}
        currentUserId={userId}
        onToggle={onToggle}
      />
      <Button variant="ghost" className="min-h-11 self-start" disabled={!workspace || toggle.isPending} onPress={() => {
        if (workspace) router.push({ pathname: "/[workspace]/issue/[id]/emoji-picker", params: { workspace, id: issue.id } });
      }}><Text>{t("reactions.add")}</Text></Button>
      {toggle.isError && <Text accessibilityRole="alert" className="text-sm text-destructive">{t("common:states.save_failed")}</Text>}
    </View>
  );
}
