# Personal inbox notification bots

Settings → Notifications → Notification bots configures independent outbound
bots for WeCom (`wecom`), Feishu (`lark`), DingTalk (`dingtalk`), Slack (`slack`)
and Telegram (`telegram`). These are not conversational integrations and never
inherit a business agent's credentials. Each destination belongs to the signed-in
user in the current workspace. Group destinations can disclose private task
content; configure only destinations the user explicitly trusts.

The server needs a stable base64-encoded 32-byte
`MULTICA_NOTIFICATION_SECRET_KEY`. Generate it once with `openssl rand -base64 32`,
store it as a deployment secret and share it across API replicas. Losing or changing
the key requires re-entering bot credentials. Apply migrations 451–454 before
starting this version. Configure `MULTICA_APP_URL` or `FRONTEND_ORIGIN` with the
public HTTPS application URL for inbox links. Without HTTPS, messages omit links.

Webhooks are restricted to each provider's official HTTPS host. WeCom, Feishu,
DingTalk and Slack use an incoming webhook. Feishu and DingTalk optionally use a
signing secret. Telegram uses a BotFather token and a chat ID (the bot must be
allowed to send to the target chat). Platform keyword/IP/signature security
settings must permit the messages. All notifications include `Multica`, which can
be used as a platform keyword. Never paste credentials into tasks, logs or docs.

Bots start disabled. Enable explicitly after configuration. Only new inbox items
after the latest activation are forwarded, following the inbox category mute
preferences. The native OS notification switch is independent. The server works
without an open desktop/web client. It scans durable inbox records every 5 seconds,
recovers up to 24 hours of new notifications and retries failures up to six times,
with increasing minute delays. Delivery is at-least-once: ambiguous provider
timeouts can duplicate a message. Stored delivery records are retained two days.
Disabling/deleting a bot prevents future queued sends; a request already in flight
cannot be recalled. Removing workspace membership also prevents queued delivery.

API (session-authenticated, workspace header required):

- `GET /api/notification-bots`: availability plus public bot metadata; no credentials.
- `POST /api/notification-bots`: name, platform, is_enabled, credentials.
- `PUT /api/notification-bots/{id}`: name, same platform, is_enabled; omit
  credentials to preserve the saved secret, or supply a complete replacement.
- `DELETE /api/notification-bots/{id}`: delete the bot and its queued deliveries.
- `POST /api/notification-bots/{id}/test`: explicitly send one fixed test message,
  even if disabled. Never call this without the user's authorization to send.

Credential fields are `webhook_url`, `secret`, `bot_token`, `chat_id`; unused fields
are empty strings. Public response fields are `id`, `name`, `platform`,
`is_enabled`, `last_delivery_at`, `last_error`. Delivery errors are sanitized.

Official setup references:

- WeCom: https://developer.work.weixin.qq.com/document/path/91770
- Feishu: https://open.feishu.cn/document/client-docs/bot-v3/add-custom-bot
- DingTalk: https://open.dingtalk.com/document/orgapp/custom-robot-access
- Slack: https://docs.slack.dev/messaging/sending-messages-using-incoming-webhooks/
- Telegram: https://core.telegram.org/bots/api#sendmessage
