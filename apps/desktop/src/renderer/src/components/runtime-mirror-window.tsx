import { useEffect, useMemo, useRef, type CSSProperties } from "react";
import { CoreProvider } from "@multica/core/platform";
import { pickLocale } from "@multica/core/i18n";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceList } from "@multica/core/workspace";
import { useQuery } from "@tanstack/react-query";
import {
  runtimeListOptions,
  runtimeDisplayLabel,
} from "@multica/core/runtimes";
import { DragStrip } from "@multica/views/platform";
import { MirrorPlatformProvider } from "@multica/views/runtimes/mirror";
import { MirrorSurface } from "@multica/views/runtimes/mirror";
import { RESOURCES } from "@multica/views/locales";
import { useT } from "@multica/views/i18n";
import { ThemeProvider } from "@multica/ui/components/common/theme-provider";
import { Button } from "@multica/ui/components/ui/button";
import { X } from "lucide-react";
import { createDesktopLocaleAdapter } from "../platform/i18n-adapter";
import type { RuntimeMirrorPreloadAPI } from "../../../preload/runtime-mirror";

export function RuntimeMirrorApp() {
  const bridge = window.runtimeMirrorAPI;
  const localeAdapter = useMemo(
    () => createDesktopLocaleAdapter(bridge?.systemLocale ?? navigator.language),
    [],
  );
  const locale = useMemo(() => pickLocale(localeAdapter), [localeAdapter]);
  if (
    !bridge?.context ||
    !bridge.runtimeConfig ||
    bridge.context.scope.backendIdentity !==
      bridge.runtimeConfig.apiUrl.replace(/\/$/, "")
  )
    return null;
  return (
    <ThemeProvider>
      <CoreProvider
        apiBaseUrl={bridge.runtimeConfig.apiUrl}
        wsUrl={bridge.runtimeConfig.wsUrl}
        identity={{ platform: "desktop" }}
        locale={locale}
        resources={{ [locale]: RESOURCES[locale] }}
        localeAdapter={localeAdapter}
        onSessionExpired={() => bridge.reportAuthSession(null)}
      >
        <FloatingMirrorContent bridge={bridge} />
      </CoreProvider>
    </ThemeProvider>
  );
}

function FloatingMirrorContent({
  bridge,
}: {
  readonly bridge: RuntimeMirrorPreloadAPI;
}) {
  const { t } = useT("runtimes");
  const user = useAuthStore((state) => state.user);
  const authStatus = useAuthStore((state) => state.status);
  const context = bridge.context;
  const { workspaces, ready } = useWorkspaceList({
    enabled: authStatus === "authenticated",
  });
  const scope = context?.scope;
  const query = useQuery({
    ...runtimeListOptions(scope?.workspaceId ?? ""),
    enabled:
      !!scope &&
      user?.id === scope.accountId &&
      workspaces.some((workspace) => workspace.id === scope.workspaceId),
  });
  const runtime = query.data?.find(
    (entry) =>
      entry.id === scope?.runtimeId &&
      entry.workspace_id === scope?.workspaceId,
  );
  const drag = useRef<{ x: number; y: number } | null>(null);
  const surface = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (authStatus === "authenticated" && user)
      bridge.reportAuthSession(user.id);
    if (authStatus === "unauthenticated") bridge.reportAuthSession(null);
  }, [authStatus, user, bridge]);
  useEffect(() => {
    const element = surface.current;
    if (!element) return;
    const pinch = (event: WheelEvent) => {
      if (!event.ctrlKey) return;
      event.preventDefault();
      void bridge.resize(window.innerWidth * Math.exp(-event.deltaY / 300));
    };
    element.addEventListener("wheel", pinch, { passive: false });
    return () => element.removeEventListener("wheel", pinch);
  }, [bridge]);
  useEffect(() => {
    if (
      authStatus === "authenticated" &&
      (user?.id !== scope?.accountId ||
        (ready &&
          !workspaces.some(
            (workspace) => workspace.id === scope?.workspaceId,
          )) ||
        (query.isSuccess && !runtime))
    )
      void bridge.close();
  }, [
    authStatus,
    user,
    scope,
    ready,
    workspaces,
    query.isSuccess,
    runtime,
    bridge,
  ]);
  return (
    <div
      ref={surface}
      className="flex h-dvh min-h-0 flex-col bg-background text-foreground"
      data-runtime-mirror-window="true"
    >
      <DragStrip />
      <header
        style={{ WebkitAppRegion: "no-drag" } as CSSProperties}
        className="flex shrink-0 items-center gap-2 border-b px-2 py-1"
        onPointerDown={(event) => {
          if (
            event.button !== 0 ||
            (event.target instanceof Element && event.target.closest("button"))
          )
            return;
          drag.current = { x: event.screenX, y: event.screenY };
          event.currentTarget.setPointerCapture(event.pointerId);
        }}
        onPointerMove={(event) => {
          if (!drag.current) return;
          const delta = {
            x: event.screenX - drag.current.x,
            y: event.screenY - drag.current.y,
          };
          drag.current = { x: event.screenX, y: event.screenY };
          void bridge.move(delta);
        }}
        onPointerUp={() => {
          drag.current = null;
        }}
        onPointerCancel={() => {
          drag.current = null;
        }}
      >
        <span
          className="min-w-0 flex-1 truncate text-caption font-medium"
          title={runtime ? runtimeDisplayLabel(runtime) : context?.title}
        >
          {runtime ? runtimeDisplayLabel(runtime) : context?.title}
        </span>
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label={t(($) => $.vscreen.close)}
          onClick={() => void bridge.close()}
        >
          <X aria-hidden="true" />
        </Button>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {runtime && scope ? (
          <MirrorPlatformProvider value={{ localControl: (scope, operation) => bridge.vscreenDesktop({ scope, operation }) }}><MirrorSurface
            key={JSON.stringify(scope)}
            scope={scope}
            runtime={runtime}
            compact
          /></MirrorPlatformProvider>
        ) : (
          <p className="p-4 text-caption text-muted-foreground" role="status">
            {t(($) => $.vscreen.preparing)}
          </p>
        )}
      </div>
    </div>
  );
}
