"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";
import { toast } from "sonner";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  clientErrorMessage,
} from "@multica/core/api";
import { useCurrentWorkspace } from "@multica/core/paths";
import type { MirrorEvent, MirrorEventType } from "@multica/core/types";
import {
  workspaceMirrorEventsOptions,
  workspaceMirrorNetworkOptions,
} from "@multica/core/workspace/queries";
import {
  useClearWorkspaceMirrorEvents,
} from "@multica/core/workspace/mutations";
import { useLocale } from "../../i18n";
import { useT } from "../../i18n";
import { SettingsCard, SettingsSection } from "./settings-layout";

function eventLabelKey(event: MirrorEventType) {
  switch (event) {
    case "session_started":
      return "session_started";
    case "session_answered":
      return "session_answered";
    case "session_failed":
      return "session_failed";
    case "viewer_started":
      return "viewer_started";
    case "viewer_stopped":
      return "viewer_stopped";
    default:
      return null;
  }
}

function formatEventTime(createdAt: string, locale: string): string {
  const date = new Date(createdAt);
  if (Number.isNaN(date.getTime())) return createdAt;
  return new Intl.DateTimeFormat(locale, {
    dateStyle: "medium",
    timeStyle: "medium",
  }).format(date);
}

export function MirrorLogPanel() {
  const { t } = useT("settings");
  const locale = useLocale();
  const workspace = useCurrentWorkspace();
  const wsId = workspace?.id ?? "";
  const eventsQuery = useQuery(workspaceMirrorEventsOptions(wsId));
  const networkQuery = useQuery(workspaceMirrorNetworkOptions(wsId));
  const clearEvents = useClearWorkspaceMirrorEvents(wsId);
  const [confirmOpen, setConfirmOpen] = useState(false);

  const events = eventsQuery.data?.events ?? [];
  const canManage = networkQuery.data?.can_manage ?? false;

  const handleClear = async () => {
    try {
      await clearEvents.mutateAsync();
      toast.success(t(($) => $.mirror_network.log.cleared));
    } catch (error) {
      toast.error(
        clientErrorMessage(error) ?? t(($) => $.mirror_network.log.clear_failed),
      );
    } finally {
      setConfirmOpen(false);
    }
  };

  return (
    <SettingsSection
      title={t(($) => $.mirror_network.log.title)}
      description={t(($) => $.mirror_network.log.description)}
      action={
        canManage && events.length > 0 ? (
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={clearEvents.isPending}
            onClick={() => setConfirmOpen(true)}
          >
            <Trash2 className="size-4" />
            {t(($) => $.mirror_network.log.clear)}
          </Button>
        ) : null
      }
    >
      {eventsQuery.isLoading ? (
        <Skeleton className="h-40 w-full" />
      ) : events.length === 0 ? (
        <SettingsCard>
          <p className="px-4 py-6 text-center text-caption text-muted-foreground">
            {t(($) => $.mirror_network.log.empty)}
          </p>
        </SettingsCard>
      ) : (
        <SettingsCard className="divide-y divide-surface-border overflow-hidden p-0">
          {events.map((event: MirrorEvent) => {
            const labelKey = eventLabelKey(event.event);
            return (
              <div
                key={event.id}
                className="flex flex-col gap-1 px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:gap-4"
              >
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
                    <span className="text-caption font-medium">
                      {event.runtime_name ||
                        t(($) => $.mirror_network.log.unknown_runtime)}
                    </span>
                    <span className="text-caption text-muted-foreground">
                      {labelKey
                        ? t(($) => $.mirror_network.log.events[labelKey])
                        : event.event}
                    </span>
                  </div>
                  {event.failure_reason ? (
                    <p className="mt-0.5 break-words text-caption text-destructive">
                      {event.failure_reason}
                    </p>
                  ) : null}
                </div>
                <span className="shrink-0 text-caption text-muted-foreground">
                  {formatEventTime(event.created_at, locale)}
                </span>
              </div>
            );
          })}
        </SettingsCard>
      )}

      <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t(($) => $.mirror_network.log.clear_confirm_title)}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.mirror_network.log.clear_confirm_description)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>
              {t(($) => $.mirror_network.log.clear_cancel)}
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={(event) => {
                event.preventDefault();
                void handleClear();
              }}
            >
              {t(($) => $.mirror_network.log.clear_confirm_action)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </SettingsSection>
  );
}
