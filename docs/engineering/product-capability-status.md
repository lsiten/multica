# 项目能力与优化实施状态

核验日期：2026-11-17。本文按当前源码、路由和已执行检查区分仓库实现、部署前提与验收证据。
“仓库已实现”不代表生产已启用，也不代表已完成原生设备或外部服务验收。

剩余工作的优先级、实施方式与验收标准见[优化收尾方案](optimization-follow-up.md)。

## 本轮优化

| 功能 | 当前行为 | 实现与验证入口 | 仍需验证 |
| --- | --- | --- | --- |
| 移动端全部任务、我的任务 | 服务端筛选与分页；显示已加载数量和服务端总数；翻页失败可重试；实时事件及重连刷新分页投影 | `apps/mobile/data/queries/issue-pages.ts`、`components/issue/paginated-issue-list.tsx`；251 条数据、筛选、去重和首次请求竞态回归 | iPhone 上滚动、筛选、删除后返回，以及 Web 改动后的跨端同步 |
| 工作流草稿 | 远端更新保留本地编辑；显式加载新版；保存携带版本，服务端锁内拒绝过期写入；新建成功允许跳转；窄屏操作区换行 | `packages/views/workflows/`、`server/internal/handler/issue_workflow_update.go`；组件、Hook 和真实数据库回归；网页冲突与桌面取消离开实测 | 原生退出、刷新及更多跨客户端场景 |
| 移动端分享链接 | 任务、项目、评论复用纯路径构造器，使用部署配置的 Web origin | `apps/mobile/components/issue/comment-context-menu.tsx`、任务/项目详情 | 真机打开与复制、非默认域名部署 |
| 应用编辑器 | Escape、取消、弹层关闭和应用导航均保护脏输入；保存期间阻止关闭；失败保留输入 | `packages/views/applications/application-editor.tsx`；真实组件回归、网页及独立桌面开发版交互 | 原生退出、刷新、更新安装退出的完整矩阵 |
| 桌面页签与窗口 | 前台页签、历史记录、工作区及深链接导航统一检查离开守卫；原生窗口先征询渲染层全部关闭监听器，拒绝时保持窗口和输入 | `apps/desktop/src/renderer/src/stores/tab-navigation-guards.ts`、`src/main/window-close-guard.ts`、`src/preload/window-close.ts`；原生关闭取消、延迟取消、继续编辑与明确放弃实测 | Cmd-Q、刷新、更新安装与多个窗口的真实操作；独立复核结论另列 |
| 搜索 | 请求失败与空结果分开；保留查询重试；已取消请求不显示错误 | `packages/views/search/use-server-search.ts` 及三个消费入口；请求失败、重试和取消回归 | 各入口的实际键盘与网络恢复 |
| 移动端任务/项目编辑 | 加载错误、无权访问、记录不存在有反馈与重试；脏表单和保存期间禁用原生下拉关闭 | `apps/mobile/app/(app)/[workspace]/issue/[id]/edit.tsx`、`project/[id]/edit.tsx` | iOS 表单、键盘、手势与错误恢复 |
| 通知偏好 | 加载/保存失败可见；重试原工作区的失败补丁；跨工作区不重放 | `apps/mobile/data/mutations/notification-preferences.ts`、通知设置页 | 真机连续切换与失败重试 |
| 移动端表情 | 无现有表情也能添加；任务与评论共用选择器内容；成功后关闭，失败保留；选中状态有无障碍语义 | `apps/mobile/components/issue/emoji-picker-body.tsx`、`reaction-bar.tsx` | 原生选择器、点击范围、读屏、两种主题 |
| 桌面操作就绪状态 | 缺少诊断数据、离线、检查失败不再静默消失；提供重新检查与原有权限修复入口 | `packages/views/runtimes/components/agent-desktop-readiness.tsx`；运行时详情回归 | 本机权限拒绝、修复后恢复及各平台真实诊断 |
| 智能体创建 | 现有创建流程保存失败留在草稿；Qoder 隐藏不兼容字段，并提前说明审批与文件回传限制 | `packages/views/agents/create/`；创建提交、草稿和执行覆盖项测试 | 已配置 Qoder 连接上的真实创建 |
| 接入引导 | 区分本地运行时、托管云智能体与独立云电脑；部署是否启用服务商单独判断 | `packages/views/locales/*/onboarding.json`；8 语言键与占位符检查 | Web/Desktop 两个引导分支 |
| 用量口径 | 运行时报表显示获取时间、查看时区、USD/积分边界；刷新失败可重试并保留已有数据 | `packages/views/runtimes/components/usage-section.tsx`；首次失败与缓存刷新失败回归 | 真实服务商延迟结算与账单对照 |
| 待处理与人工请求 | 复用项目 Attention 面板、任务上下文和现有人工请求卡；请求修订、指定响应人、步骤、影响与下一步由原有协议控制 | `packages/views/issues/components/work-progress-panel.tsx`、`common/human-request-card.tsx`、`apps/mobile/components/human-requests/`；相关测试通过 | 跨端回复及过期/权限变化场景 |
| iOS 后台推送 | 原生 APNs：配置就绪、明确授权、令牌注册/退出清理、持久队列、按偏好与权限投递、通知跳转与已读同步；删除工作区清理其投递记录并保留账号设备及其他工作区记录 | `server/internal/mobilepush/`、`server/internal/handler/mobile_push*.go`、`apps/mobile/data/mobile-push-*`；HTTP/2 签名、真实数据库与客户端生命周期回归 | Apple 凭据、带推送签名的实体设备、前后台与冷启动收发 |
| iOS 分发与更新 | EAS TestFlight/生产配置；独立环境与递增构建号；通过签名二进制分发更新 | [原生推送与分发](../../apps/mobile/docs/push-and-release.md) | 所属 EAS/Apple 项目绑定、签名 IPA、TestFlight 上传、覆盖安装；未配置 OTA |
| 多项目聊天文件面板 | 当前项目与可用项目分组；项目文件、源码管理、当前附件、项目问题分 Tab；未选择项目时不读取任何 worktree；单一聊天 worktree 可直接作为源码范围；切换项目清理目录、搜索和 Git 上下文；浮动聊天和文档标题都可拖入桌面 Tab 栏提升为独立聊天页签；文件操作 pending scope 包含 workspace、runtime、task 和根路径；移动/恢复 receipt 更新打开文档，回收关闭旧 Tab 但保留草稿 | `server/internal/handler/chat.go`、`packages/core/chat/file-directories.ts`、`packages/core/chat/document-tab-drag.ts`、`packages/core/chat/workspace-file-operation-store.ts`、`packages/core/chat/workspace-file-document-state.ts`、`packages/views/chat/components/doc-file-list.tsx`、`packages/views/chat/components/chat-window.tsx`、`apps/desktop/src/renderer/src/components/tab-bar.tsx`；多项目选择隔离、独立源码 Tab、无 Git 空状态、单 worktree 初始范围、HTML5 拖放、指针提升、文档拖拽提升、操作身份隔离和文件 receipt 连续性回归；桌面端已实际看到四个独立 Tab 及“当前聊天没有可用的 Git 工作区”空状态 | 多项目桌面全流程、项目失效后的原生提示、真实 Git 目录切换矩阵、原生拖放自动化工具的 HTML5 DataTransfer 限制仍需真实鼠标/设备复核 |

## 现有能力不能再笼统列为“未实现”

| 能力 | 已有实现 | 实际边界与后续工作 |
| --- | --- | --- |
| 飞书 / Lark | `server/internal/integrations/lark/` 包含区域识别、接入、入站处理和回传；服务端有实际路由 | 需部署密钥、对应区域应用和权限；国际版与飞书需分别验收，不能由单一代码存在推断已部署 |
| 语音转文字 | `server/internal/handler/chat_voice.go`、daemon voice 通道和转写执行器；校验执行权限、运行时能力与在线状态 | 需转写程序、麦克风许可和支持该能力的运行时；不等于所有聊天通道都支持语音消息 |
| 品牌资源生成 | `scripts/brand/README.md`、`packages/ui/lib/multica-brand.ts` 统一生成品牌资源 | 属于构建资源管理；没有据此确认完整的租户级白标产品流程。品牌授权遵循仓库实际许可条件 |
| Qoder 用量 | `server/internal/qoderruntime/usage.go` 记录服务商积分，`TaskUsage.credits_micros` 与运行详情单独显示 | 会话使用量快照不是最终账单；不杜撰积分兑美元汇率；运行时 token/USD 聚合不代表 Qoder 完整账单 |
| agent-desktop | 任务注入、诊断及授权人工接管已存在 | [统一桌面操作方案](agent-desktop-computer-use.md) 明确列出生产迁移与平台验收缺口；仓库实现与生产验收分开记录 |

## Qoder 的当前合同

`server/cmd/qoder-runtime/README.md` 与 `server/internal/qoderruntime/` 是当前实现依据：

- 已有任务、聊天及小组负责人执行；文本、工具事件、恢复游标、积分记录，以及聊天产出文件回传。
- 模型、工具与技能在 Qoder 侧配置。Multica 的自定义技能、客户端工具、终端和交互式工具审批不是此连接的能力。
- `requires_action` 当前会按停止原因结束为失败，不会自动批准。任务运行产出文件回传与聊天文件回传的支持范围不同。
- 新增审批或任务文件交付应单独验证服务商 API、授权主体、可恢复游标和幂等交付，再实施协议与 UI。不能只添加按钮并声称能力已打通。

## 独立项目与验收顺序

Web 连接本机 daemon、额外服务商、Linux 原生镜像、Android 客户端继续按独立项目评估，
不与本轮缺陷修复混为一个“已完成”状态：

1. Web 本机连接需要确定本地服务发现、身份绑定、浏览器权限和安装恢复；现有 CLI/桌面接入流程仍可使用。
2. 额外服务商需先有实际执行、取消、恢复、用量和权限合同，再接入现有运行时协议。
3. Linux 原生镜像在 `server/internal/mirror/capture_support_unsupported.go` 明确不支持；通用 agent-desktop 跨平台能力不能当作 Linux 镜像已实现。
4. 移动端目前按 iOS 构建与验收；Android 需要独立的构建、权限、导航、推送通道及设备矩阵。

交付顺序为：完成当前回归 → Web/Desktop 真实交互 → iOS 原生交互与跨端同步 → APNs 真机收发 →
签名分发与覆盖更新 → 生产部署验证。外部凭据、设备与审批结果须有实际证据，不能由测试替代。

## 验证记录

已通过的定向检查覆盖移动端分页/通知注册、工作流并发、应用编辑器、搜索、运行时诊断、
用量、人工请求与项目关注面板。移动端全套 49 个文件、387 项测试及 iOS 构建脚本测试通过；
类型检查、lint、依赖目录检查与 121 项脚本测试通过。工作流、推送与通知偏好的真实数据库
定向回归在 race 检查下通过。迁移套件与受保护的智能体套件单独复跑通过。

网页的应用放弃修改、工作流冲突、搜索失败、运行时诊断和用量五个区域，经两轮独立只读复核。
当前 22 张截图覆盖浅色/深色及 390、1440px，页面布局补充 1920px；新增文本的抽样对比度
最低为 4.77:1。修复了实测发现的新建跳转被拦截、返回/复制重复确认、窄屏标题和面包屑挤压。
这些结果只覆盖已观察的网页区域，不代表完整的无障碍或原生平台认证。

迁移已在本地管理环境执行，未配置 Apple 凭据时，实际 APNs 配置接口返回 `enabled:false`。
Expo config introspection 确认启用 APNs 时生成开发环境 entitlement。Helm 默认配置与可选
APNs Secret 只读挂载的模板检查通过，尚未在集群安装。

共享界面此前曾有一轮 725 个文件、8651 项测试通过（383.61 秒）。本轮先在组合检查中发现
聊天测试 logger mock 缺少 `noopLogger` 导出，以及评论失败上传占位未清理；两项均已修复。
随后 Views 全量复跑通过：734 个测试文件、8692 项测试全部通过（509.37 秒）。类型检查、
核心上传/草稿与聊天上传定向回归也通过。
最新桌面完整回归通过：121 个文件、1194 项
测试，另有 2 个文件、2 项测试跳过。核心逻辑 295 个文件、3639 项测试通过，1 项跳过；
Web 46 个文件、413 项测试通过。共享界面执行期间修改的聊天文件创建测试另行定向复跑，
2 项通过。后续并行改动又追加定向回归：核心 40 项、共享界面 52 项、桌面 13 项通过。
最新类型检查 9 项任务通过（5 项命中缓存），lint 6 项任务通过，保留现有警告、无错误。
旧迁移索引测试超时后，完整迁移套件单独复跑通过；并行 SQL 变更期间的生成代码缺失已同步。

Go handler 回归已通过。固定账号和工作区夹具的跨进程清理由数据库会话锁隔离；队列驱动
在另一 API worker 持有租约时等待对应投递的最终状态；Webhook、迁移回填和未读消息续接
定向回归均通过。workspace file coordination 新增的三张表已补入 workspace deletion
manifest，删除流程与文件操作清理保持一致。

Jev 的临时表夹具复用了公共表的已缓存查询计划，数据库探针已复现；改用独立连接后，
三项既有回归及后续完整运行中的 Jev 用例均通过。此前未读智能体消息重放和延后会话晋升
两个精确用例均通过（2.803 秒）；在恢复 Colima/PostgreSQL 并正确导出工作区 `.env` 后，
handler 完整套件最终通过：789.728 秒。
与本轮功能直接相关的创建/更新项目上下文、项目删除清理和聊天文件投影测试已在同一数据库
环境单独复跑通过（30.723 秒），包括多项目显式选择、无选择阻断、Agent/Squad 目录解析、
工作区隔离和已删除 owner 过滤。

已直接捕获开发数据库时钟回退约 400 毫秒。本地 Colima/Lima 每 10 秒执行强制校时，
日志中的两次正向偏差与失败发生在同一秒；[Lima 对应版本实现](https://github.com/lima-vm/lima/blob/de0816ea4bdc5267b428ab21025889b8dd785526/pkg/guestagent/api/server/server.go#L84-L107)
会把超过阈值的偏差通过直接设时修正。具体失败行的完整因果链仍需验证，不能仅据同秒
关联就把全部失败归为环境。后续先验证共享虚拟机时钟稳定性，再做完整 handler 验收；
调整该虚拟机的计时会影响全部容器，临时验证方案等待单独确认。

补充只读对照中，主机经过 30.42 秒，虚拟机单调时间经过 31.13 秒，相差约 710 毫秒，
超过约 64 毫秒的测量界限。这支持进一步验证虚拟机计时源，尚不能证明切换计时源就是修复。

完整回归还发现并修复了删除工作区后的推送投递记录遗漏：新增实际删除回归先失败、后
通过；保留账号设备和其他工作区记录，相关 race 回归通过。历史失败与复跑记录均保留，
不能把定向通过等同于整包通过。

独立桌面开发版已实测工作流和应用的原生窗口关闭取消、延迟取消与保留输入；应用内
“继续编辑”也保留输入，明确确认后退出临时表单。未创建真实应用或工作流。此证据不
涵盖 Cmd-Q、原生刷新、更新安装或多窗口退出矩阵。

第三轮独立复核提出的文档及项目编辑器关闭守卫缺口已修复：文档表面、项目执行范围和
项目监督设置统一注册共享导航守卫，使用明确的继续编辑/保留草稿文案，保存期间阻止关闭，
并移除重复的独立 `beforeunload` 监听器。相关组件与导航守卫回归已通过；原生开发版仍需
验证窗口关闭、页签切换、Cmd-Q、刷新和多窗口矩阵，原生放弃修改按钮的完整截图证据也仍待补齐。

本轮五张原生截图的实际编码为 JPEG，已将原先不匹配的 PNG 扩展名修正为 JPEG 扩展名，
未转换或修改像素。深色、最小窗口、200% 缩放、动效及其他未覆盖项继续保留为证据缺口。

原生设备、Apple 投递、签名分发、新用户引导及已配置 Qoder 的真实界面验收尚未完成。
当前机器只有 Command Line Tools，未发现 Xcode 的 `simctl`；后续需要
开发者团队、目标 Bundle ID、P8 服务端密钥配置，以及实体设备或所属 EAS 项目。完整历史
截图矩阵、动效与减少动态效果、200% 缩放等未覆盖项保留为验收缺口。
