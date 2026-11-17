import { createContext, useContext, useEffect, useRef, useState } from "react";
import { AppState, Platform } from "react-native";
import { router, useRootNavigationState } from "expo-router";
import Constants from "expo-constants";
import * as Application from "expo-application";
import * as Notifications from "expo-notifications";
import { queryOptions, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";
import { useAuthStore } from "./auth-store";
import { MobilePushSession, type PushNative, type PushStatus } from "./mobile-push-session";
import { pushNavigationTarget } from "@/lib/push-navigation";
import { refreshInboxList, refreshInboxUnreadSummary } from "./realtime/inbox-ws-updaters";

const native: PushNative = {
  async availability() {
    if (Platform.OS !== "ios") return "unsupported";
    if (Constants.expoConfig?.extra?.nativeAPNs !== true) return "build_required";
    const environment = await Application.getIosPushNotificationServiceEnvironmentAsync();
    if (!environment || !Application.applicationId) return "build_required";
    return { topic: Application.applicationId, environment: environment === "development" ? "sandbox" : "production" };
  },
  async permission(request) {
    const current = await Notifications.getPermissionsAsync();
    const permission = request && current.canAskAgain && !current.granted
      ? await Notifications.requestPermissionsAsync({ ios: { allowAlert: true, allowSound: true, allowBadge: true } })
      : current;
    if (permission.granted || permission.ios?.status === Notifications.IosAuthorizationStatus.PROVISIONAL) return "granted";
    return permission.canAskAgain ? "undetermined" : "denied";
  },
  async token() {
    const token = await Notifications.getDevicePushTokenAsync();
    if (token.type !== "ios" || typeof token.data !== "string") throw new Error("Native APNs token unavailable");
    return token.data;
  },
  async unregister() {
    if (Platform.OS !== "ios") return;
    await Notifications.unregisterForNotificationsAsync();
    await Notifications.dismissAllNotificationsAsync();
    Notifications.clearLastNotificationResponse();
  },
};

interface PushContextValue {
  readonly status: PushStatus;
  readonly refresh: (requestPermission?: boolean) => Promise<void>;
}
const PushContext = createContext<PushContextValue | null>(null);

export function MobilePushProvider({ children }: { children: React.ReactNode }) {
  const qc = useQueryClient();
  const userId = useAuthStore((state) => state.user?.id);
  const navigation = useRootNavigationState();
  const handled = useRef<string | null>(null);
  const [status, setStatus] = useState<PushStatus>("checking");
  const [session] = useState(() => new MobilePushSession({
    native,
    configuration: () => qc.fetchQuery(queryOptions({
      queryKey: ["mobile-push", "configuration", useAuthStore.getState().user?.id],
      queryFn: ({ signal }) => api.getMobilePushConfiguration({ signal }),
      staleTime: 0, retry: false,
    })),
    register: (device, signal) => api.registerMobilePushDevice(device, signal),
    remove: (device) => api.deleteMobilePushDevice(device),
    onStatus: setStatus,
  }));

  useEffect(() => {
    const unsubscribe = useAuthStore.subscribe((state, previous) => {
      if (state.user?.id !== previous.user?.id) session.setUser(state.user?.id ?? null);
    });
    session.setUser(useAuthStore.getState().user?.id ?? null);
    const appState = AppState.addEventListener("change", (state) => {
      if (state === "active") void session.refresh();
    });
    const tokenSubscription = Notifications.addPushTokenListener(() => { void session.refresh(); });
    return () => {
      unsubscribe();
      appState.remove();
      tokenSubscription.remove();
      session.setUser(null);
    };
  }, [session]);

  useEffect(() => {
    const open = (response: Notifications.NotificationResponse | null) => {
      const user = useAuthStore.getState().user;
      if (!navigation?.key || !response || !user || response.actionIdentifier !== Notifications.DEFAULT_ACTION_IDENTIFIER) return;
      const identifier = response.notification.request.identifier;
      if (identifier === handled.current) return;
      const data = response.notification.request.content.data;
      const target = pushNavigationTarget(data, user.id);
      handled.current = identifier;
      Notifications.clearLastNotificationResponse();
      if (target) {
        router.push(target);
        if (typeof data?.inbox_id === "string" && data.inbox_id) {
          void api.markInboxRead(data.inbox_id, target.params.workspace).then((item) => {
            if (useAuthStore.getState().user?.id !== user.id) return;
            if (item.workspace_id) void refreshInboxList(qc, item.workspace_id);
            void refreshInboxUnreadSummary(qc);
          }).catch(() => { console.warn("[push] Could not mark the opened notification as read"); });
        }
      }
    };
    const subscription = Notifications.addNotificationResponseReceivedListener(open);
    open(Notifications.getLastNotificationResponse());
    return () => subscription.remove();
  }, [navigation?.key, userId, qc]);

  return <PushContext.Provider value={{ status, refresh: (request) => session.refresh(request) }}>{children}</PushContext.Provider>;
}

export function useMobilePush() {
  const context = useContext(PushContext);
  if (!context) throw new Error("MobilePushProvider is required");
  return context;
}
