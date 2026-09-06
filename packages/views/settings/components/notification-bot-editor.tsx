"use client";

import { useId, useState } from "react";
import {
  notificationBotPlatforms,
  NotificationBotPlatformSchema,
  type NotificationBot,
  type SaveNotificationBot,
} from "@multica/core/notification-bots/schema";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Switch } from "@multica/ui/components/ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n";
import { NotificationBotSecretInput } from "./notification-bot-secret-input";

export function NotificationBotEditor({
  bot,
  pending,
  onSave,
  onClose,
}: {
  readonly bot: NotificationBot | null;
  readonly pending: boolean;
  readonly onSave: (input: SaveNotificationBot) => void;
  readonly onClose: () => void;
}) {
  const { t } = useT("settings");
  const prefix = useId();
  const [name, setName] = useState(bot?.name ?? "");
  const [platform, setPlatform] = useState(bot?.platform ?? "wecom");
  const [isEnabled, setEnabled] = useState(bot?.isEnabled ?? false);
  const [replace, setReplace] = useState(!bot);
  const [webhookURL, setWebhook] = useState("");
  const [secret, setSecret] = useState("");
  const [botToken, setToken] = useState("");
  const [chatID, setChat] = useState("");
  const telegram = platform === "telegram";
  const signed = platform === "lark" || platform === "dingtalk";

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !pending) onClose();
      }}
    >
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>
            {t(($) =>
              bot
                ? $.notification_bots.editTitle
                : $.notification_bots.newTitle,
            )}
          </DialogTitle>
          <DialogDescription>
            {t(($) => $.notification_bots.privacy)}
          </DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault();
            if (pending) return;
            onSave({
              ...(bot ? { id: bot.id } : {}),
              name: name.trim(),
              platform,
              isEnabled,
              ...(replace
                ? {
                    credentials: {
                      webhookURL: telegram ? "" : webhookURL.trim(),
                      secret: signed ? secret.trim() : "",
                      botToken: telegram ? botToken.trim() : "",
                      chatID: telegram ? chatID.trim() : "",
                    },
                  }
                : {}),
            });
          }}
        >
          <div className="space-y-2">
            <Label htmlFor={`${prefix}-name`}>
              {t(($) => $.notification_bots.name)}
            </Label>
            <Input
              id={`${prefix}-name`}
              value={name}
              required
              maxLength={100}
              disabled={pending}
              onChange={(event) => setName(event.target.value)}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${prefix}-platform`}>
              {t(($) => $.notification_bots.platform)}
            </Label>
            <Select
              items={notificationBotPlatforms.map((value) => ({
                value,
                label: t(($) => $.notification_bots[value]),
              }))}
              value={platform}
              disabled={!!bot || pending}
              onValueChange={(value) => {
                const parsed = NotificationBotPlatformSchema.safeParse(value);
                if (parsed.success) {
                  setPlatform(parsed.data);
                  setWebhook("");
                  setSecret("");
                  setToken("");
                  setChat("");
                }
              }}
            >
              <SelectTrigger id={`${prefix}-platform`} className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {notificationBotPlatforms.map((value) => (
                  <SelectItem key={value} value={value}>
                    {t(($) => $.notification_bots[value])}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          {bot && (
            <div className="space-y-2">
              <div className="flex items-center justify-between gap-3">
                <Label htmlFor={`${prefix}-replace`}>
                  {t(($) => $.notification_bots.credentials)}
                </Label>
                <Switch
                  id={`${prefix}-replace`}
                  checked={replace}
                  disabled={pending}
                  onCheckedChange={setReplace}
                />
              </div>
              <p className="text-caption text-muted-foreground">
                {t(($) => $.notification_bots.keep)}
              </p>
            </div>
          )}
          {replace && (
            <fieldset disabled={pending} className="space-y-4">
              <p className="text-caption text-muted-foreground">
                {t(($) => $.notification_bots.replaceHint)}
              </p>
              {telegram ? (
                <>
                  <NotificationBotSecretInput
                    label={t(($) => $.notification_bots.token)}
                    required
                    value={botToken}
                    onValueChange={setToken}
                  />
                  <div className="space-y-2">
                    <Label htmlFor={`${prefix}-chat`}>
                      {t(($) => $.notification_bots.chat)}
                    </Label>
                    <Input
                      id={`${prefix}-chat`}
                      required
                      maxLength={128}
                      value={chatID}
                      onChange={(event) => setChat(event.target.value)}
                    />
                  </div>
                </>
              ) : (
                <NotificationBotSecretInput
                  label={t(($) => $.notification_bots.webhook)}
                  required
                  maxLength={2048}
                  value={webhookURL}
                  onValueChange={setWebhook}
                />
              )}
              {signed && (
                <NotificationBotSecretInput
                  label={t(($) => $.notification_bots.secret)}
                  value={secret}
                  onValueChange={setSecret}
                />
              )}
            </fieldset>
          )}
          <div className="flex items-center justify-between gap-3">
            <Label htmlFor={`${prefix}-enabled`}>
              {t(($) => $.notification_bots.enabled)}
            </Label>
            <Switch
              id={`${prefix}-enabled`}
              checked={isEnabled}
              disabled={pending}
              onCheckedChange={setEnabled}
            />
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={pending}
              onClick={onClose}
            >
              {t(($) => $.notification_bots.cancel)}
            </Button>
            <Button type="submit" disabled={pending || !name.trim()}>
              {t(($) => $.notification_bots.save)}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
