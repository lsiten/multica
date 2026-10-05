import { useState } from "react";
import { ActivityIndicator, Alert, View } from "react-native";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { HumanRequest, HumanRequestAnswer } from "@multica/core/types";
import { Button } from "@/components/ui/button";
import { Text } from "@/components/ui/text";
import { AutosizeTextArea } from "@/components/ui/autosize-textarea";
import { useWorkspaceStore } from "@/data/workspace-store";
import { humanRequestKeys, humanRequestOptions, useHumanRequestSync, useRespondHumanRequest } from "@/data/queries/human-requests";
import { useT } from "@/lib/i18n";

export function HumanRequestCard({ requestId, fallbackContent }: { requestId: string; fallbackContent?: string }) {
  const workspaceId = useWorkspaceStore(s => s.currentWorkspaceId);
  const query = useQuery(humanRequestOptions(workspaceId, requestId));
  useHumanRequestSync(query.data);
  const { t } = useT("common");
  if (!query.data) return <View className="gap-2">
    {fallbackContent && <Text>{fallbackContent}</Text>}
    {query.isPending ? <ActivityIndicator accessibilityLabel={t("human_request.loading")} /> : <>
      <Text accessibilityRole="alert">{t("human_request.load_failed")}</Text>
      <Button variant="outline" onPress={() => void query.refetch()}><Text>{t("human_request.retry")}</Text></Button>
    </>}
  </View>;
  return <HumanRequestContent key={`${query.data.id}:${query.data.revision}`} request={query.data} />;
}

function HumanRequestContent({ request }: { request: HumanRequest }) {
  const { t } = useT("common");
  const [answer, setAnswer] = useState("");
  const [detailsOpen, setDetailsOpen] = useState(false);
  const submit = useRespondHumanRequest(request.id);
 const client = useQueryClient();
 const workspaceId = useWorkspaceStore(s => s.currentWorkspaceId);
  const payload = request.payload;
  const pending = request.status === "pending" && Date.parse(request.expires_at) > Date.now();
  const actionable = pending && request.can_respond && payload.kind !== "unknown";
  const send = (decision: HumanRequestAnswer["decision"], value?: string) => submit.mutate({ revision: request.revision, decision, ...(value ? { answer: value } : {}) });
  const primary = () => {
    if (payload.kind === "confirmation") {
      Alert.alert(payload.title, payload.impact || payload.next, [{ text: t("human_request.decline"), style: "cancel" }, { text: payload.action_label, onPress: () => send("approve") }]);
      return;
    }
    send(payload.kind === "manual" ? "completed" : "input", payload.kind === "input" ? answer : undefined);
  };
  const status = request.status === "answered" ? t("human_request.received") : request.status === "declined" ? t("human_request.declined") : request.status === "cancelled" ? t("human_request.cancelled") : request.status === "expired" || !pending && request.status === "pending" ? t("human_request.expired") : t("human_request.unavailable");
  return <View className="gap-3 rounded-lg border border-border bg-card p-4" accessibilityLabel={payload.title}>
    <Text className="font-semibold text-foreground">{payload.title}</Text>
    {pending && <>
      {payload.steps.map((step, index) => <Text key={index}>{index+1}. {step}</Text>)}
      {payload.impact && <Text>{payload.impact}</Text>}
      <Text className="text-sm text-muted-foreground">{payload.next}</Text>
    </>}
    {payload.details && <>
      <Button variant="ghost" onPress={() => setDetailsOpen(!detailsOpen)} accessibilityState={{ expanded: detailsOpen }}><Text>{t("human_request.details")}</Text></Button>
      {detailsOpen && <Text className="text-sm text-muted-foreground">{payload.details}</Text>}
    </>}
    {!pending && <Text accessibilityLiveRegion="polite" className="text-sm text-muted-foreground">{status}</Text>}
    {pending && !actionable && <Text className="text-sm text-muted-foreground">{payload.kind === "unknown" ? t("human_request.unavailable") : t("human_request.other_member")}</Text>}
    {actionable && <>
      {payload.kind === "input" && <View className="gap-1"><Text>{payload.input_label}</Text><AutosizeTextArea accessibilityLabel={payload.input_label} value={answer} onChangeText={setAnswer} editable={!submit.isPending} maxLength={2000} /></View>}
      <View className="gap-2">
        {payload.kind === "choice" ? payload.choices.map(choice => <Button key={choice.id} variant={choice.recommended ? "default" : "outline"} disabled={submit.isPending} onPress={() => send("choice", choice.id)}><Text>{choice.label}{choice.recommended ? ` · ${t("human_request.recommended")}` : ""}</Text></Button>) : <Button disabled={submit.isPending || payload.kind === "input" && !answer.trim()} onPress={primary}><Text>{payload.action_label}</Text></Button>}
        <Button variant="outline" disabled={submit.isPending} onPress={() => send("reject")}><Text>{t("human_request.decline")}</Text></Button>
      </View>
      {submit.isPending && <ActivityIndicator accessibilityLabel={t("human_request.submitting")} />}
      {submit.isError && <View className="gap-2"><Text accessibilityRole="alert" className="text-destructive">{t("human_request.submit_failed")}</Text><Button variant="outline" onPress={() => { submit.reset(); void client.invalidateQueries({ queryKey: humanRequestKeys.detail(workspaceId, request.id) }); }}><Text>{t("human_request.refresh")}</Text></Button></View>}
    </>}
  </View>;
}
