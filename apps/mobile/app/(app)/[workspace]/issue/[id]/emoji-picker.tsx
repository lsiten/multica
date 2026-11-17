import { ActivityIndicator, View } from "react-native";
import { router, useLocalSearchParams } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import { EmojiPickerBody } from "@/components/issue/emoji-picker-body";
import { Button } from "@/components/ui/button";
import { Text } from "@/components/ui/text";
import { issueDetailOptions } from "@/data/queries/issues";
import { useToggleIssueReaction } from "@/data/mutations/issues";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useAuthStore } from "@/data/auth-store";
import { useT } from "@/lib/i18n";

export default function IssueEmojiPicker() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const workspaceId = useWorkspaceStore((state) => state.currentWorkspaceId);
  const userId = useAuthStore((state) => state.user?.id);
  const { t } = useT("common");
  const query = useQuery(issueDetailOptions(workspaceId, id));
  const toggle = useToggleIssueReaction(id);

  if (!query.data) return <View className="flex-1 items-center justify-center gap-3 px-4">
    {query.isError ? <><Text accessibilityRole="alert" className="text-sm text-destructive">{t("states.load_failed")}</Text><Button variant="outline" onPress={() => { void query.refetch(); }}><Text>{t("actions.retry")}</Text></Button></> : <ActivityIndicator />}
  </View>;

  return <EmojiPickerBody pending={toggle.isPending} failed={toggle.isError} onSelect={(emoji) => {
    const existing = query.data.reactions?.find((reaction) => reaction.emoji === emoji && reaction.actor_type === "member" && reaction.actor_id === userId);
    toggle.mutate({ emoji, existing }, { onSuccess: () => router.back() });
  }} />;
}
