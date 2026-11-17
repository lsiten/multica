import { ActivityIndicator, Linking, View } from "react-native";
import { useMobilePush } from "@/data/mobile-push-provider";
import { useT } from "@/lib/i18n";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";

export function PushRegistration() {
  const { status, refresh } = useMobilePush();
  const { t } = useT("settings");
  return <View className="gap-3 rounded-md border border-border bg-card px-4 py-3">
    <Text className="text-base font-medium">{t("push.title")}</Text>
    <Text accessibilityRole={status === "error" ? "alert" : undefined} className="text-sm text-muted-foreground">{t(`push.${status}`)}</Text>
    {status === "checking" && <ActivityIndicator />}
    {status === "permission_denied" ? <Button variant="outline" onPress={() => { void Linking.openSettings(); }}><Text>{t("push.open_settings")}</Text></Button>
      : status === "permission_required" ? <Button onPress={() => { void refresh(true); }}><Text>{t("push.enable")}</Text></Button>
      : (status === "error" || status === "server_unavailable" || status === "registered") ? <Button variant="outline" onPress={() => { void refresh(); }}><Text>{t("push.check_again")}</Text></Button>
      : null}
  </View>;
}
