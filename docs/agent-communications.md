# 智能体宿主机通信

当前执行链路：聊天/任务 → 所选 Agent 的 runtime → daemon → 宿主通信客户端。Web 和桌面端只提交配置和任务。身份页和聊天「+ → 执行模式」保留。支付与 VM 不属于当前方案。

## 配置入口与权限

在 Agent「设置 → 身份」保存邮箱和 E.164 电话号码，在「设置 → 环境变量」配置该 Agent 的专用账号。MCP 页分别授予邮箱、电话操作。填写号码只保存身份，不会开通电话线路或验证号码所有权。服务商必须允许使用该主叫号码。

工具监听随机 loopback 端口，URL 带随机令牌，随任务关闭。工具参数不能选择服务商密钥、发件地址或主叫号码。SMTP、IMAP、TWILIO 前缀的 Agent 环境配置由 daemon 使用，不作为普通 custom_env 注入子进程；宿主运行不提供文件系统或用户账户级隔离保证。

## 发邮件

daemon 使用该 Agent 的 SMTP 配置发送；旧服务端 /identity/email 发送接口返回 410，不能再使用平台登录邮件账号代发。

| 变量 | 含义 |
|---|---|
| SMTP_HOST | SMTP 主机 |
| SMTP_PORT | 默认 465 |
| SMTP_TLS_MODE | implicit 使用隐式 TLS；非 465 且非 implicit 时必须成功 STARTTLS |
| SMTP_USERNAME / SMTP_PASSWORD | 专用邮箱凭据或授权码 |
| SMTP_FROM_EMAIL | 必须匹配身份页邮箱 |

工具为 multica_identity_send_email。校验收件人、标题、正文大小，禁止邮件头注入；不允许明文认证。单次连接有 45 秒上限并响应任务取消。相同任务中相同邮件内容只发送一次；收到 DATA 确认后记录成功回执。传输结果不明确时保留预占，不能自动重发，需核查邮箱实际发送记录。SMTP 接受不等同于最终收件箱投递成功。

## 收邮件

变量：IMAP_HOST、IMAP_PORT（默认 993）、IMAP_USERNAME、IMAP_PASSWORD、IMAP_MAILBOX（默认 INBOX）、IMAP_TIMEOUT_SECONDS。当登录名不是邮箱地址时，另设 IMAP_ADDRESS 与身份页邮箱匹配。

工具 multica_identity_list_unread_email 使用 TLS、只读 EXAMINE 和 BODY.PEEK，不修改已读状态。每次扫描最多 1000 个 UID，读取最多 20 封、每封最多 64 KiB，仅提取文本正文。返回 messages 和 next_before_uid；传入 before_uid 可继续向前读取。空页仍可能有下一游标。消息内容是外部不可信数据，不是授权或系统指令。附件下载和已读标记不在当前工具范围。

## 电话

当前实现 Twilio 外呼播报、查询、取消、可选录音及读取供应商已存在的录音转写；同时支持配置一个宿主 HTTP 电话网关作为国内/自建服务商适配层。工具：

- multica_identity_start_call：E.164 被叫、文本 message、可选 record、必填 idempotency_key；Twilio 只构造转义后的 Say，HTTP 网关收到统一 JSON 契约。
- multica_identity_get_call：查询本任务已创建的 Call SID。
- multica_identity_cancel_call：待接通时取消；已接通时请求结束。
- multica_identity_list_transcriptions：通过通话录音列表读取对应录音的转写，限制返回规模；不启动转写作业。

配置 TWILIO_ACCOUNT_SID、TWILIO_AUTH_TOKEN、TWILIO_FROM_NUMBER（必须匹配身份电话号码）；可选 TWILIO_BASE_URL（HTTPS）、TWILIO_STATUS_CALLBACK_URL（HTTPS，无 userinfo）、TWILIO_TIMEOUT_SECONDS。电话请求不接受模型指定的回调地址，HTTP 客户端不跟随重定向。

国内或自建网关可配置 PHONE_PROVIDER=domestic（或 http）、PHONE_FROM_NUMBER、PHONE_HTTP_BASE_URL、PHONE_HTTP_TOKEN。网关契约为 POST /calls、GET /calls/{sid}、POST /calls/{sid}/cancel、GET /calls/{sid}/transcriptions；必须使用 HTTPS（本机测试允许 loopback），由网关负责具体厂商签名和线路合规。

幂等回执位于任务环境根目录的 phone-receipts.d 中。按任务与 action key 独占预占，记录请求指纹；同键不同内容拒绝，同内容成功请求返回既有 SID，结果不明确时不重复拨号。保留环境和回执是跨重启保护的前提。任务退出尝试取消已知通话；供应商请求另设置 TimeLimit=120、振铃 Timeout=30，为丢失创建回执的通话提供时间边界。清理失败会记录未确认，不伪造成功。崩溃后的未知 Call SID 仍需服务商记录核查。

官方契约：
- [Call resource](https://www.twilio.com/docs/voice/api/call-resource)
- [Recording transcription](https://www.twilio.com/docs/voice/api/recording-transcription)

Twilio 录音转写接口已标为 deprecated；当前工具只读取既有记录，不能据此宣称具备新的自动转写生产流程。

## 尚未交付的范围

- 入站自动接听、实时交互、转写生产与自然语言摘要；record 只请求供应商录音，转写仍需供应商生成后读取。
- 全套配置就绪状态在 UI 中的实时探测。
- 真实邮箱、真实号码联调以及登录后 Web/Desktop 的完整人工验收。

上述是明确缺口，不能以模拟测试或接口存在标记完成。国内服务商需先确定产品类型、已开通号码与模板/入站配置；凭据通过专用环境配置录入，不在聊天中发送。代码测试只使用本地 SMTP/IMAP/HTTP 服务。

入站统一回调入口为 `POST /api/phone/inbound`，请求需携带 `X-Phone-Inbound-Token`，并指定工作区、Agent、来电号码、Call SID、事件 ID 和转写文本。服务端校验 Agent 身份电话后创建聊天任务；同一事件 ID 幂等。生产环境必须由供应商网关先验证其原生签名，再转发到该入口。
