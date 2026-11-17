/**
 * Full emoji picker for a comment reaction — opened from the per-comment
 * long-press menu's "+" tapback button. Mirrors web's emoji-mart picker
 * that sits behind QuickEmojiPicker's overflow button: same product
 * semantics (mobile must offer the full emoji set, not only the 8 quick
 * picks).
 *
 * Reads the comment from the timeline cache to detect an already-applied
 * reaction by the current user, then fires `useToggleCommentReaction` with
 * the right `existing` value so re-tapping an active emoji removes it
 * (matches web behaviour and the inline ReactionBar toggle semantics).
 *
 * Library: `rn-emoji-keyboard` (TheWidlarzGroup/rn-emoji-keyboard). We
 * embed the `EmojiKeyboard` component (no built-in modal) inside the
 * Expo Router formSheet route body, so the iOS UISheetPresentationController
 * still owns the chrome (grabber, detents, drag-to-dismiss).
 */
import { useCallback, useMemo } from "react";
import { useLocalSearchParams, router } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import type { Reaction } from "@multica/core/types";
import { EmojiPickerBody } from "@/components/issue/emoji-picker-body";
import { issueTimelineOptions } from "@/data/queries/issues";
import { useToggleCommentReaction } from "@/data/mutations/issues";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";

export default function CommentEmojiPickerRoute() {
  const { id, commentId } = useLocalSearchParams<{
    id: string;
    commentId: string;
  }>();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const userId = useAuthStore((s) => s.user?.id);
  const toggle = useToggleCommentReaction(id);

  const { data: timeline = [] } = useQuery(issueTimelineOptions(wsId, id));
  const entry = useMemo(
    () => timeline.find((e) => e.id === commentId) ?? null,
    [timeline, commentId],
  );

  const reactions = useMemo<Reaction[]>(
    () => (entry?.reactions ?? []) as Reaction[],
    [entry?.reactions],
  );

  const onSelect = useCallback(
    (emoji: string) => {
      const existing = reactions.find(
        (r) =>
          r.emoji === emoji &&
          r.actor_type === "member" &&
          r.actor_id === userId,
      );
      toggle.mutate({ commentId, emoji, existing }, { onSuccess: () => router.back() });
    },
    [reactions, userId, toggle, commentId],
  );

  return <EmojiPickerBody onSelect={onSelect} pending={toggle.isPending} failed={toggle.isError} />;
}
