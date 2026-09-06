"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { useRequiredWorkspaceSlug } from "@multica/core/paths";
import {
  notificationBotOptions,
  useNotificationBotAction,
} from "@multica/core/notification-bots/queries";
import type { NotificationBot } from "@multica/core/notification-bots/schema";
import { Button } from "@multica/ui/components/ui/button";
import { Switch } from "@multica/ui/components/ui/switch";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@multica/ui/components/ui/dialog";
import { toast } from "sonner";
import { useT } from "../../i18n";
import { SettingsCard, SettingsRow, SettingsSection } from "./settings-layout";
import { NotificationBotEditor } from "./notification-bot-editor";

export function NotificationBotsSection() {
  const workspaceId = useWorkspaceId();
  // Remounting prevents an open credential form crossing workspace boundaries.
  return (
    <WorkspaceNotificationBots key={workspaceId} workspaceId={workspaceId} />
  );
}

function WorkspaceNotificationBots({
  workspaceId,
}: {
  readonly workspaceId: string;
}) {
  const { t } = useT("settings");
  const slug = useRequiredWorkspaceSlug();
  const query = useQuery(notificationBotOptions(workspaceId, slug));
  const action = useNotificationBotAction(workspaceId, slug);
  const [editor, setEditor] = useState<{
    readonly bot: NotificationBot | null;
  } | null>(null);
  const [deleting, setDeleting] = useState<NotificationBot | null>(null);
  const available = query.data?.available ?? false;
  const showError = (error: Error) => toast.error(error.message);

  return (
    <SettingsSection
      title={t(($) => $.notification_bots.title)}
      description={t(($) => $.notification_bots.description)}
    >
      {query.isPending ? (
        <p role="status" className="text-caption text-muted-foreground">
          {t(($) => $.notification_bots.loading)}
        </p>
      ) : query.isError ? (
        <div role="alert" className="space-y-2">
          <p className="text-caption text-destructive">
            {t(($) => $.notification_bots.error)}
          </p>
          <Button variant="outline" onClick={() => void query.refetch()}>
            {t(($) => $.notification_bots.retry)}
          </Button>
        </div>
      ) : (
        <>
          {!available && (
            <p
              role="status"
              className="break-words text-caption text-muted-foreground"
            >
              {t(($) => $.notification_bots.unavailable)}
            </p>
          )}
          <SettingsCard>
            {!query.data?.bots.length && (
              <p className="p-4 text-caption text-muted-foreground">
                {t(($) => $.notification_bots.empty)}
              </p>
            )}
            {query.data?.bots.map((bot) => (
              <SettingsRow
                key={bot.id}
                label={<span className="break-words">{bot.name}</span>}
                description={
                  <>
                    <span>{t(($) => $.notification_bots[bot.platform])}</span>
                    {bot.lastError && (
                      <p className="mt-1 text-destructive">
                        {t(($) => $.notification_bots.failed)}
                      </p>
                    )}
                  </>
                }
              >
                <div className="flex flex-wrap items-center gap-2">
                  <Switch
                    checked={bot.isEnabled}
                    aria-label={`${bot.name}: ${t(($) => $.notification_bots.enabled)}`}
                    disabled={!available || action.isPending}
                    onCheckedChange={(isEnabled) =>
                      action.mutate(
                        {
                          kind: "save",
                          input: {
                            id: bot.id,
                            name: bot.name,
                            platform: bot.platform,
                            isEnabled,
                          },
                        },
                        { onError: showError },
                      )
                    }
                  />
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={!available || action.isPending}
                    onClick={() => setEditor({ bot })}
                  >
                    {t(($) => $.notification_bots.edit)}
                  </Button>
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={!available || action.isPending}
                    onClick={() =>
                      action.mutate(
                        { kind: "test", id: bot.id },
                        {
                          onSuccess: () =>
                            toast.success(
                              t(($) => $.notification_bots.testSuccess),
                            ),
                          onError: showError,
                        },
                      )
                    }
                  >
                    {t(($) => $.notification_bots.test)}
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={action.isPending}
                    onClick={() => setDeleting(bot)}
                  >
                    {t(($) => $.notification_bots.remove)}
                  </Button>
                </div>
              </SettingsRow>
            ))}
          </SettingsCard>
          <Button
            variant="outline"
            disabled={!available || action.isPending}
            onClick={() => setEditor({ bot: null })}
          >
            {t(($) => $.notification_bots.add)}
          </Button>
        </>
      )}
      {editor && (
        <NotificationBotEditor
          bot={editor.bot}
          pending={action.isPending}
          onClose={() => setEditor(null)}
          onSave={(input) =>
            action.mutate(
              { kind: "save", input },
              { onSuccess: () => setEditor(null), onError: showError },
            )
          }
        />
      )}
      <Dialog
        open={!!deleting}
        onOpenChange={(open) => {
          if (!open && !action.isPending) setDeleting(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
          <DialogTitle className="min-w-0 break-words pr-8">{deleting?.name}</DialogTitle>
            <DialogDescription>
              {t(($) => $.notification_bots.confirmDelete)}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              disabled={action.isPending}
              onClick={() => setDeleting(null)}
            >
              {t(($) => $.notification_bots.cancel)}
            </Button>
            <Button
              variant="destructive"
              disabled={action.isPending}
              onClick={() => {
                if (deleting)
                  action.mutate(
                    { kind: "delete", id: deleting.id },
                    { onSuccess: () => setDeleting(null), onError: showError },
                  );
              }}
            >
              {t(($) => $.notification_bots.remove)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </SettingsSection>
  );
}
