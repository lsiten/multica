import { z } from "zod";

export const notificationBotPlatforms = [
  "wecom",
  "lark",
  "dingtalk",
  "slack",
  "telegram",
] as const;
export const NotificationBotPlatformSchema = z.enum(notificationBotPlatforms);
export type NotificationBotPlatform = z.infer<
  typeof NotificationBotPlatformSchema
>;

export const NotificationBotSchema = z
  .object({
    id: z.string().min(1),
    name: z.string(),
    platform: NotificationBotPlatformSchema,
    is_enabled: z.boolean(),
    last_delivery_at: z.string().nullable().optional(),
    last_error: z.string().default(""),
  })
  .transform((bot) => ({
    id: bot.id,
    name: bot.name,
    platform: bot.platform,
    isEnabled: bot.is_enabled,
    lastDeliveryAt: bot.last_delivery_at ?? null,
    lastError: bot.last_error,
  }));
export type NotificationBot = z.infer<typeof NotificationBotSchema>;
export const NotificationBotListSchema = z.object({
  available: z.boolean(),
  bots: z.array(NotificationBotSchema),
});
export type NotificationBotList = z.infer<typeof NotificationBotListSchema>;

export interface NotificationBotCredentials {
  readonly webhookURL: string;
  readonly secret: string;
  readonly botToken: string;
  readonly chatID: string;
}
export interface SaveNotificationBot {
  readonly id?: string;
  readonly name: string;
  readonly platform: NotificationBotPlatform;
  readonly isEnabled: boolean;
  readonly credentials?: NotificationBotCredentials;
}
