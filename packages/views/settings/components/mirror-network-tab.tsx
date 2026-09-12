"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, Check, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Alert, AlertDescription, AlertTitle } from "@multica/ui/components/ui/alert";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  clientErrorMessage,
} from "@multica/core/api";
import { useCurrentWorkspace } from "@multica/core/paths";
import type {
  MirrorNetworkMode,
  MirrorNetworkServerInput,
  MirrorNetworkSettings,
} from "@multica/core/types";
import { workspaceMirrorNetworkOptions } from "@multica/core/workspace/queries";
import { useUpdateWorkspaceMirrorNetwork } from "@multica/core/workspace/mutations";
import { useT } from "../../i18n";
import {
  SettingsCard,
  SettingsRow,
  SettingsSection,
  SettingsTab,
} from "./settings-layout";

interface CustomServerDraft {
  /** One ICE URL per line. */
  urls: string;
  username: string;
  credential: string;
  /** True when a credential is already stored server-side. */
  hasCredential: boolean;
}

function emptyCustomDraft(): CustomServerDraft {
  return { urls: "", username: "", credential: "", hasCredential: false };
}

function settingsToDraft(settings: MirrorNetworkSettings): CustomServerDraft[] {
  if (settings.custom.length === 0) {
    return [emptyCustomDraft()];
  }
  return settings.custom.map((server) => ({
    urls: server.urls.join("\n"),
    username: server.username ?? "",
    credential: "",
    hasCredential: server.has_credential,
  }));
}

function collectServers(drafts: CustomServerDraft[]): MirrorNetworkServerInput[] {
  const servers: MirrorNetworkServerInput[] = [];
  for (const draft of drafts) {
    const urls = draft.urls
      .split(/[\n,]/)
      .map((value) => value.trim())
      .filter(Boolean);
    if (urls.length === 0) continue;
    const server: MirrorNetworkServerInput = {
      urls,
      ...(draft.username.trim() ? { username: draft.username.trim() } : {}),
      ...(draft.credential ? { credential: draft.credential } : {}),
    };
    servers.push(server);
  }
  return servers;
}

export function MirrorNetworkTab() {
  const { t } = useT("settings");
  const workspace = useCurrentWorkspace();
  const wsId = workspace?.id ?? "";
  const networkQuery = useQuery(workspaceMirrorNetworkOptions(wsId));
  const settings = networkQuery.data;
  const update = useUpdateWorkspaceMirrorNetwork(wsId);

  const [mode, setMode] = useState<MirrorNetworkMode>("builtin");
  const [drafts, setDrafts] = useState<CustomServerDraft[]>([emptyCustomDraft()]);

  // Re-seed the editor whenever the server state changes (initial load and
  // every successful save). Do not derive render state directly from the
  // query: the user is allowed to hold unsaved edits.
  useEffect(() => {
    if (!settings) return;
    setMode(settings.mode);
    setDrafts(settingsToDraft(settings));
  }, [settings]);

  const readOnly = !settings || settings.locked || !settings.can_manage;
  const hasTurn = settings?.turn_configured ?? false;
  const builtinAvailable = settings?.builtin.available ?? false;

  const previewServers = useMemo(
    () => collectServers(drafts),
    [drafts],
  );
  const customHasTurn = previewServers.some((server) =>
    (Array.isArray(server.urls) ? server.urls : [server.urls]).some(
      (url) => url.startsWith("turn:") || url.startsWith("turns:"),
    ),
  );

  if (networkQuery.isLoading || !settings) {
    return (
      <SettingsTab title={t(($) => $.mirror_network.title)}>
        <Skeleton className="h-40 w-full" />
      </SettingsTab>
    );
  }

  const save = async () => {
    try {
      if (mode === "custom") {
        const servers = collectServers(drafts);
        if (servers.length === 0 || !customHasTurn) {
          toast.error(t(($) => $.mirror_network.errors.requires_turn));
          return;
        }
        await update.mutateAsync({ mode, servers });
      } else {
        await update.mutateAsync({ mode });
      }
      toast.success(t(($) => $.mirror_network.saved));
    } catch (error) {
      toast.error(
        clientErrorMessage(error) ?? t(($) => $.mirror_network.errors.save),
      );
    }
  };

  const sourceLabel = (() => {
    switch (settings.source) {
      case "env":
        return t(($) => $.mirror_network.sources.env);
      case "builtin":
        return t(($) => $.mirror_network.sources.builtin);
      case "custom":
        return t(($) => $.mirror_network.sources.custom);
      case "disabled":
        return t(($) => $.mirror_network.sources.disabled);
      default:
        return t(($) => $.mirror_network.sources.builtin_unavailable);
    }
  })();

  return (
    <SettingsTab
      title={t(($) => $.mirror_network.title)}
      description={t(($) => $.mirror_network.description)}
    >
      {settings.locked ? (
        <Alert>
          <AlertTriangle />
          <AlertTitle>{t(($) => $.mirror_network.locked_title)}</AlertTitle>
          <AlertDescription>
            {t(($) => $.mirror_network.locked_description)}
          </AlertDescription>
        </Alert>
      ) : null}

      {!hasTurn ? (
        <Alert variant="destructive">
          <AlertTriangle />
          <AlertTitle>{t(($) => $.mirror_network.no_turn_title)}</AlertTitle>
          <AlertDescription>
            {t(($) => $.mirror_network.no_turn_description)}
          </AlertDescription>
        </Alert>
      ) : null}

      <SettingsSection title={t(($) => $.mirror_network.status_title)}>
        <SettingsCard>
          <SettingsRow
            label={t(($) => $.mirror_network.active_source)}
            description={sourceLabel}
          >
            {hasTurn ? (
              <Badge variant="secondary" className="gap-1">
                <Check className="size-3" />
                {t(($) => $.mirror_network.turn_ready)}
              </Badge>
            ) : (
              <Badge variant="outline" className="gap-1 text-muted-foreground">
                <AlertTriangle className="size-3" />
                {t(($) => $.mirror_network.turn_missing)}
              </Badge>
            )}
          </SettingsRow>
          {settings.builtin.enabled ? (
            <SettingsRow
              label={t(($) => $.mirror_network.builtin_host)}
              description={
                settings.builtin.host
                  ? `${settings.builtin.host}${
                      settings.builtin.port ? `:${settings.builtin.port}` : ""
                    } · ${(settings.builtin.transports ?? []).join(" / ").toUpperCase()}`
                  : t(($) => $.mirror_network.builtin_host_unset)
              }
            >
              <Badge
                variant={builtinAvailable ? "secondary" : "outline"}
                className={builtinAvailable ? "gap-1 text-success" : "text-muted-foreground"}
              >
                {builtinAvailable
                  ? t(($) => $.mirror_network.builtin_available)
                  : t(($) => $.mirror_network.builtin_unavailable)}
              </Badge>
            </SettingsRow>
          ) : null}
        </SettingsCard>
      </SettingsSection>

      <SettingsSection
        title={t(($) => $.mirror_network.mode_title)}
        description={t(($) => $.mirror_network.mode_description)}
      >
        <SettingsCard>
          <SettingsRow
            label={t(($) => $.mirror_network.modes.builtin_label)}
            description={
              settings.builtin.available
                ? t(($) => $.mirror_network.modes.builtin_description)
                : t(($) => $.mirror_network.modes.builtin_unavailable_description)
            }
          >
            <input
              type="radio"
              name="mirror-network-mode"
              value="builtin"
              checked={mode === "builtin"}
              disabled={readOnly}
              onChange={() => setMode("builtin")}
              aria-label={t(($) => $.mirror_network.modes.builtin_label)}
            />
          </SettingsRow>
          <SettingsRow
            label={t(($) => $.mirror_network.modes.custom_label)}
            description={t(($) => $.mirror_network.modes.custom_description)}
            align="start"
          >
            <input
              type="radio"
              name="mirror-network-mode"
              value="custom"
              checked={mode === "custom"}
              disabled={readOnly}
              onChange={() => setMode("custom")}
              aria-label={t(($) => $.mirror_network.modes.custom_label)}
            />
          </SettingsRow>
          {mode === "custom" ? (
            <div className="space-y-4 px-4 py-4">
              {drafts.map((draft, index) => (
                <div
                  key={index}
                  className="space-y-2 rounded-lg border border-surface-border p-3"
                >
                  <div className="flex items-center justify-between gap-2">
                    <Label className="text-caption">
                      {t(($) => $.mirror_network.custom.server_index, {
                        index: index + 1,
                      })}
                    </Label>
                    {drafts.length > 1 ? (
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        disabled={readOnly}
                        aria-label={t(($) => $.mirror_network.custom.remove)}
                        onClick={() =>
                          setDrafts((current) =>
                            current.filter((_, item) => item !== index),
                          )
                        }
                      >
                        <Trash2 className="size-4" />
                      </Button>
                    ) : null}
                  </div>
                  <div className="space-y-1">
                    <Label className="text-caption text-muted-foreground">
                      {t(($) => $.mirror_network.custom.urls)}
                    </Label>
                    <textarea
                      className="min-h-20 w-full rounded-md border border-input bg-background px-3 py-2 font-mono text-caption focus-visible:outline-2 focus-visible:outline-ring disabled:opacity-60"
                      value={draft.urls}
                      disabled={readOnly}
                      placeholder={t(
                        ($) => $.mirror_network.custom.urls_placeholder,
                      )}
                      onChange={(event) =>
                        setDrafts((current) =>
                          current.map((item, itemIndex) =>
                            itemIndex === index
                              ? { ...item, urls: event.target.value }
                              : item,
                          ),
                        )
                      }
                    />
                  </div>
                  <div className="grid gap-2 sm:grid-cols-2">
                    <div className="space-y-1">
                      <Label className="text-caption text-muted-foreground">
                        {t(($) => $.mirror_network.custom.username)}
                      </Label>
                      <Input
                        value={draft.username}
                        disabled={readOnly}
                        autoComplete="off"
                        aria-label={t(($) => $.mirror_network.custom.username)}
                        onChange={(event) =>
                          setDrafts((current) =>
                            current.map((item, itemIndex) =>
                              itemIndex === index
                                ? { ...item, username: event.target.value }
                                : item,
                            ),
                          )
                        }
                      />
                    </div>
                    <div className="space-y-1">
                      <Label className="text-caption text-muted-foreground">
                        {t(($) => $.mirror_network.custom.credential)}
                      </Label>
                      <Input
                        type="password"
                        value={draft.credential}
                        disabled={readOnly}
                        autoComplete="new-password"
                        aria-label={t(($) => $.mirror_network.custom.credential)}
                        placeholder={
                          draft.hasCredential
                            ? t(($) => $.mirror_network.custom.credential_saved)
                            : undefined
                        }
                        onChange={(event) =>
                          setDrafts((current) =>
                            current.map((item, itemIndex) =>
                              itemIndex === index
                                ? { ...item, credential: event.target.value }
                                : item,
                            ),
                          )
                        }
                      />
                    </div>
                  </div>
                  {draft.hasCredential ? (
                    <p className="text-caption text-muted-foreground">
                      {t(($) => $.mirror_network.custom.credential_kept)}
                    </p>
                  ) : null}
                </div>
              ))}
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={readOnly || drafts.length >= 8}
                onClick={() =>
                  setDrafts((current) => [...current, emptyCustomDraft()])
                }
              >
                <Plus className="size-4" />
                {t(($) => $.mirror_network.custom.add)}
              </Button>
            </div>
          ) : null}
          <SettingsRow
            label={t(($) => $.mirror_network.modes.disabled_label)}
            description={t(($) => $.mirror_network.modes.disabled_description)}
          >
            <input
              type="radio"
              name="mirror-network-mode"
              value="disabled"
              checked={mode === "disabled"}
              disabled={readOnly}
              onChange={() => setMode("disabled")}
              aria-label={t(($) => $.mirror_network.modes.disabled_label)}
            />
          </SettingsRow>
        </SettingsCard>
      </SettingsSection>

      {!readOnly ? (
        <div className="flex items-center gap-3">
          <Button onClick={save} disabled={update.isPending}>
            {t(($) => $.mirror_network.save)}
          </Button>
          {mode === "custom" && !customHasTurn ? (
            <span className="text-caption text-muted-foreground">
              {t(($) => $.mirror_network.custom.needs_turn)}
            </span>
          ) : null}
        </div>
      ) : null}
    </SettingsTab>
  );
}
