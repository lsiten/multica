import { ActivityIndicator, View } from "react-native";
import { EmojiKeyboard } from "rn-emoji-keyboard";
import { Text } from "@/components/ui/text";
import { useColorScheme } from "@/lib/use-color-scheme";
import { useT } from "@/lib/i18n";
import { THEME } from "@/lib/theme";

export function EmojiPickerBody({ onSelect, pending, failed }: {
  readonly onSelect: (emoji: string) => void;
  readonly pending: boolean;
  readonly failed: boolean;
}) {
  const { t } = useT("issues");
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  return <View className="flex-1">
    <View className="gap-2 px-4 pb-2 pt-3">
      <Text className="text-lg font-semibold text-foreground">{t("reactions.add")}</Text>
      {pending && <ActivityIndicator accessibilityLabel={t("common:states.saving")} />}
      {failed && <Text accessibilityRole="alert" className="text-sm text-destructive">{t("common:states.save_failed")}</Text>}
    </View>
    <View className="flex-1" pointerEvents={pending ? "none" : "auto"} accessibilityElementsHidden={pending}>
      <EmojiKeyboard onEmojiSelected={(picked) => onSelect(picked.emoji)} enableSearchBar enableRecentlyUsed categoryPosition="top" theme={{
        backdrop: theme.background, knob: theme.mutedForeground, container: theme.popover, header: theme.foreground,
        skinTonesContainer: theme.secondary,
        category: { icon: theme.mutedForeground, iconActive: theme.foreground, container: theme.popover, containerActive: theme.secondary },
        search: { background: theme.secondary, text: theme.foreground, placeholder: theme.mutedForeground, icon: theme.mutedForeground },
        customButton: { icon: theme.mutedForeground, iconPressed: theme.foreground, background: theme.secondary, backgroundPressed: theme.muted },
        emoji: { selected: theme.secondary },
      }} />
    </View>
  </View>;
}
