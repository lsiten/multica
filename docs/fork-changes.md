# lsiten/multica Fork 改动总说明

> 本文档汇总 fork 仓库 `lsiten/multica` 相对于上游 `multica-ai/multica` 的全部实质性改动，
> 用于交接、评审、升级对账与发布决策。
>
> - 统计基线：上游 `9b1ab4f9e`（`MUL-8546: docs(changelog): release 0.8.0`，最近一次上游同步点）
> - 历史统计快照为 `ed807eac4`（`main`，2026-11-07）。
> - 本轮于 2026-11-13 核验 `codex/macos-fork-optimization`，提交锚点为 `b14297232`，并检查本轮工作区改动。
> - 统计口径：历史提交数取 `git log ed807eac4 --not 9b1ab4f9e`，文件差异取 `git diff 9b1ab4f9e ed807eac4`
> - 历史快照中的桌面版本为 `1.0.118`；根工程/文档站版本：`0.2.0`

---

## 1. 概览

以下规模统计沿用 2026-11-07 快照。本轮优先处理 macOS，Windows 控制能力保留为独立需求。功能状态与剩余环境条件见 [需求文档 §19](fork-requirements.md#19-已知缺口与待办gap)，验证结论限定到实际执行的场景。


| 指标 | 数值 |
| --- | --- |
| fork 独有提交 | 648（含 50 条 merge 提交） |
| 净差异 | 3,540 个文件，+329,173 / −118,347 行 |
| 主要新增后端模块 | `mirror`、`vscreen`、`runtimeproc`、`daemonws`、`jevmodels`、`application`、`applicationgateway`、`communications` |
| 主要新增前端模块 | `views/runtimes`（镜像 UI）、`views/chat`、`views/settings`、`views/platform`、`views/applications`、`views/collaboration` |
| 发布通道 | Fork Images（容器镜像）+ Fork Desktop Release（三平台安装包） |
| 历史发布快照（2026-11-07） | `v1.0.118`，24 个资产，Latest |

**一句话定位**：上游 Multica 是「人与智能体协作的命令面板」，本 fork 在其之上把
「本地智能体运行时」扩展成**可视（屏幕镜像）、可控（远程/代操作）、可评审（本地 MR）、
可自托管、可独立分发**的一体化工作台。

---

## 2. 与上游的能力差异矩阵

| 能力域 | 上游行为 | fork 行为 | 主要落点 |
| --- | --- | --- | --- |
| 运行时可见性 | 仅健康状态、配置、关联智能体 | 每个 runtime 唯一屏幕镜像，WebRTC 点对点，服务端不经手像素 | `server/internal/mirror`、`packages/views/runtimes/components/mirror` |
| 远程交互 | 无 | 人工鼠标/键盘/文本注入 + 指挥智能体代操作，FCFS 仲裁显示资源 | `mirror/input_arbiter.go`、`vscreen` |
| 虚拟屏 | 无 | 原生显示宿主、有界媒体 IPC、lease、窗口复用、本地选窗 | `server/internal/vscreen` |
| 会话形态 | 对话流 | 文档化对话、内容版本化、选区工具栏与同一会话输入区 | `packages/views/chat` |
| 语音输入 | 无 | runtime 承载的按住说话 + 流式听写；桌面端离线 whisper | `apps/desktop`、`views/chat` |
| 代码评审 | 走上游评审流程 | 本地 MR：runtime 持有的暂存区、选择性文件合并事务 | `LOCAL_MR_*.md`、`views` 评审组件 |
| 决策模型 | 单一执行路径 | Jev 三来源（local / agent_context / remote），按 daemon 安装 | `server/internal/jevmodels`、`llm2jev` |
| 桌面分发 | 上游 Release 产物 | fork 自有版本线（1.0.x）+ 自有更新源 | `fork-desktop.yml` |
| 部署镜像 | 官方镜像 | 自建 GHCR 镜像 + Helm | `fork-images.yml`、`deploy/helm` |
| CI 发布链路 | 单一 `release.yml` | 双通道，上游 `release.yml` 在 fork 中整体跳过 | `.github/workflows/*` |

---

## 3. 核心模块详解

### 3.1 运行时镜像（`server/internal/mirror`，76 个 Go 文件 / 31 个测试文件）

设计文档：`docs/prd/runtime-mirroring.md`、`docs/prd/runtime-mirror-control.md`。

- **采集**：daemon 所在主机原生捕屏，支持 macOS 与 Windows；Linux 首版不支持，入口显式说明。
- **传输**：WebRTC 直连观看端，服务端不转发、不存储任何像素、截图或录像。
- **授权**：`authorization.go` 定义镜像会话授权，操作种类包含 `mcp`；`control_grant.go` + `control_grant_store.go` 管理限时可续的控制授予。
- **仲裁**：`input_arbiter.go` 按「显示资源 + 原子手势」做先到先得（FCFS）；抢不到的一方立即收到 `busy` 并各自重试，不冻结任一方。
- **审批**：`approval_broker.go` 把 Codex 的真实审批 RPC 路由到 peer 决策，而不是在本地伪造结果。
- **网络**：`cloudflare_turn.go` 与 `ice_config.go` 支持 TURN 中继；内置 TURN 默认关闭（`feat(deploy): make built-in TURN relay opt-in`），避免默认引入中继成本与隐私面。
- **前端**：`packages/views/runtimes/components/mirror/` 下有帧重组（`frame-reassembler`）、手势与坐标映射、控制条、聊天浮层、授权提示、controller presence、handoff 等组件，均有对应测试。
- **观看指标**：WebRTC 接收端按单会话串行采样 inbound video、选中候选对 RTT、解码帧、码率和区间丢包率；统计在会话关闭或采样失败时清空，页面保留协商 FPS 作为稳定回退。原生性能夹具当前停在 `native host: needs_intervention`，没有把失败误报为媒体性能通过。

隐私边界是硬约束：**服务端不经手像素，也不经手输入 payload**。

### 3.2 虚拟屏与应用窗口控制（`server/internal/vscreen`）

- 子包：`native/`（原生显示宿主）、`hostclient/`（宿主客户端）、`appclaim/`（应用窗口认领）、`smokefixture/`（冒烟夹具）。
- 能力：原生显示宿主与有界媒体 IPC；runtime lease 与原生所有权；受租约约束的应用控制与有界快照；跨任务复用托管窗口；本地用户选择既有应用窗口并由运行时认领；PID 输入策略绑定到已校验的原生身份。
- 桌面端配套：`apps/desktop/scripts/` 下有 20+ 个 `vscreen-*.mjs` 脚本，覆盖原生冒烟、远程 headless、性能度量、协议与传输验证。

### 3.3 Chat 文档化协作（`packages/views/chat`，31 个新增文件）

- 文档模式已挂载到 `ChatPage`，任务预览、附件与运行目录文件可从文件面板打开。文档替换消息正文区，原会话输入、提示与排队任务继续挂载。
- 选区工具栏与整篇引用接入同一会话输入。编辑开始时冻结正文、任务 `owner_revision` 或文件 `edit_base`，查询刷新保留草稿，版本差异显示历史内容的增删。
- 语音包含 runtime 转写与桌面离线适配。本轮修复重新订阅后的状态刷新、监听恢复和取消清理，真实麦克风及模型准备仍按安装环境验收。
- **输入体验**：Codex 风格输入历史导航；项目文件、issue、附件多 Tab；群组协作上下文与来源控制。`ChatPage` 恢复单 agent 直聊和多 agent 群聊入口，多选后保留 lead agent 并立即写入列表缓存。
- 文件面板在桌面宽布局默认展开、紧凑布局默认收起，可手动切换。Tab 按需加载，目录分页并按需展开。提交事件与运行完成定向刷新正文及版本。

### 3.4 Daemon 与运行时隔离（`server/internal/daemon`，585 个 Go 文件 / 320 个测试文件）

- **多进程**：生产 profile 强制五个子进程 owner；能力缺失、启动失败或 worker 授权不确定时 fail-closed，不回落到进程内 provider。
- **隔离**：并发工作区隔离与共享投递结算；agent 准入隔离并按配置容量填充调度。
- **可靠性**：内置 MCP broker 保活；MCP 诊断与探测归属绑定到单次 run；报告重试相互隔离，失败任务不会拖住 agent；任务结果在 409 冲突围栏处被隔离；重建清理后恢复工作目录同步。
- **工作树**：安全的自动/手动 worktree 清理，剔除已完全合并的本地目录分支，隐藏已移除的本地清单项。总览集中在桌面端管理器，议题详情保留关联上下文，Settings > Code 移除重复总览。
- **GUI 工具**：连接托管 GUI 工具与已停止运行的交接；干预报告跨重连持久化。

配套：`runtimeproc`（33 文件，进程提交与准入错误分类）、`daemonws`（22 文件，daemon WebSocket hub 与断连观测）。

### 3.5 本地评审 / 本地 MR

- 文档：`LOCAL_MR_ACCEPTANCE.md`（验收口径）、`LOCAL_MR_IMPLEMENTATION.md`（实现状态）。
- 能力：runtime 持有的暂存区、暂存 diff 预览、选择性文件合并事务、待合并选择与批量文件控制、本地评审闭环与目标模式。
- 安全要点：MR mutation 的 ID 生成走 core 的 `createSafeId`（`randomUUID` 不可用时回退 `getRandomValues`，无安全随机源则拒绝下发），而不是直接调用 `crypto.randomUUID`。

### 3.6 Jev 决策模型（`server/internal/jevmodels` + `internal/daemon/llm2jev*`）

- 模型：`Mapika/decider-2b`，固定 revision `533964dae8be954c5b5e19fa4948e48408094c1e`，推理引擎 `1.8.1`。
- 安装：桌面端提供模型目录与按需下载；安装包不含权重；安装/升级/仅切换选项都不触发下载；安装与就绪状态逐 daemon 记录，不能用 A 机器的状态授权 B 机器运行。
- 三来源：`local`（本机模型，记录模型/revision/设备/校准元数据）、`agent_context`（复用当前 agent 上下文，不另起模型进程）、`remote`。
- 默认 `agent_context`，避免未经选择就下载模型或外发上下文。
- `llm2jev` MCP：耐久决策日志与可搜索历史、置信度/概率元数据、任务结果验证元数据、能力协商引导。
- 设计说明：`docs/jev-model-sources.md`（明确标注为需求设计，非全量已实现声明）。

### 3.7 应用托管（`server/internal/application`、`applicationgateway`）

- `application`（15 文件）：应用实例生命周期，支持删除已确认停止的实例。
- `applicationgateway`（12 文件）：HMAC + JWT 的网关鉴权与 URL 校验。

### 3.8 桌面端（`apps/desktop`，108 个文件 / +7,226 行）

- **品牌与后端**：可配置后端地址与应用品牌（`brand-dev-electron.mjs`、`feat(desktop): configure backend and app branding`）。
- **可观测**：统一进程总览面板（资源占用 + 服务配置）、本地 daemon worker 进程状态面板。
- **工作树**：按 agent 分组的工作树管理、标签页刷新。
- **打包**：`bundle-cli.mjs`（三平台打包 Go CLI）、`mac-update-floor.cjs`（更新源与最低版本门槛）、`package.mjs`、`package-line-endings.test.mjs`。
- **图标规范**：桌面端与 views 统一从 `@multica/ui/icons` 引入，不再直接 `import ... from "lucide-react"`。

### 3.9 Squad 协作协议

运营协议新增四条硬规则提交：
`delivery-closure`（交付闭环）、`version-anchor`（版本锚定）、`board-tracking`（看板跟踪）、`review-loop-closure`（评审环闭合）。

### 3.10 通信渠道

- `server/internal/communications`（13 文件）：出站通信抽象与投递。
- WeCom 媒体链路：流式解密落临时文件、带内容嗅探的预览、临时文件生命周期管理（Windows 语义见 §8）。
- Lark 出站媒体仍有实现与环境前提待完成，相关方案见 `docs/lark-outbound-media.md`。Typing 反应清理的范围见 `docs/lark-typing-cleanup.md`。

---

## 4. 构建与发布

### 4.1 双通道

| 工作流 | 触发 | 产物 | 说明 |
| --- | --- | --- | --- |
| `fork-images.yml` | `v*.*.*` tag | GHCR `backend` / `web` 镜像 | 同提交全量 CI 通过后构建两种架构，backend arm64 使用 QEMU，web arm64 使用原生 runner。全部成功后发布版本与 `sha-<full>` manifest，稳定 tag 更新 `latest` |
| `fork-desktop.yml` | `v*.*.*` tag + `workflow_dispatch` | DMG / ZIP / EXE / AppImage / DEB / RPM + 更新元数据 | 同提交全量 CI 通过后运行三平台矩阵，每平台 x64 + arm64 |
| `release.yml`（上游） | — | — | `if: github.repository != 'lsiten/multica'`，在 fork 中整体跳过，避免重复发镜像与桌面产物 |

### 4.2 历史桌面产物快照（`v1.0.118`，24 个资产）

| 平台 | 架构 | 格式 |
| --- | --- | --- |
| macOS | x64 / arm64 | `.dmg`、`.zip`（各带 `.blockmap`） |
| Windows | x64 / arm64 | NSIS `.exe`（各带 `.blockmap`） |
| Linux | x86_64 / arm64 | `.AppImage`、`.deb`、`.rpm` |
| 更新元数据 | — | `latest.yml`、`latest-mac.yml`、`latest-x64-mac.yml`、`latest-arm64.yml`、`latest-linux.yml`、`latest-linux-arm64.yml` |

发布先调用同一提交的 `ci.yml` 并强制全量范围。`frontend` 和 `backend` 汇总检查通过、检出 SHA 校验一致后输出 `accepted_sha`，构建按该 SHA 检出。所有平台成功后，资产上传到 draft Release，再转为公开。上传失败保留可重试 draft，已公开版本的重复发布被拒绝。

现有 CI 的 `frontend-test` 已覆盖桌面包。本轮增加发布对该全量验收的依赖，并保留各平台打包检查。本地夹具验证 SHA 不匹配、验收不完整、tag、签名环境与发布资产边界，GitHub Actions 和真实安装升级仍需发布环境验收。

### 4.3 版本策略

- 桌面端使用独立版本线 `1.0.x`，与上游 `0.x` 解耦；打包前 `fork-desktop.yml` 校验从 Git 派生的打包版本与 tag 一致。
- 根工程与文档站仍跟随上游 `0.2.0`。
- 发布 = 在目标提交上打 `vX.Y.Z` tag 并推送；prerelease 用 `vX.Y.Z-rc.1`；`-dirty` tag 被拒绝。

### 4.4 并发与重跑（fork 定制）

- 并发组按 tag 隔离：`fork-desktop-release-${{ github.ref_name }}`，避免一个卡死的 run 阻塞后续所有发布。
- 支持 `workflow_dispatch`（必须用 tag ref 派发），便于在不移动 tag 的情况下重跑。
- 历史记录中的 Release 保留集合为 `v1.0.118` / `v1.0.115` / `v1.0.114` + `v0.1.71` / `v0.1.70` / `v0.1.69`。

---

## 5. 自托管部署

- `SELF_HOSTING_FORK.md`：fork 专用部署路径。**上游安装器与官方镜像不含本 fork 的自定义功能**，必须使用 `ghcr.io/lsiten/multica-backend` 与 `ghcr.io/lsiten/multica-web`。
- `docker-compose.selfhost.images.yml`：按版本 tag 拉取镜像。
- `deploy/helm`（4 文件 / +225 行）：Helm chart 部署。
- `.env` 关键项：`MULTICA_BACKEND_IMAGE`、`MULTICA_WEB_IMAGE`、`MULTICA_IMAGE_TAG`、`FRONTEND_ORIGIN`、`MULTICA_APP_URL`、`MULTICA_PUBLIC_URL`。旧值会覆盖默认值，升级时必须显式更新。
- 新 GHCR 包默认私有：要么在包设置里改为公开，要么让服务器用具备 `read:packages` 的凭据登录。

---

## 6. 上游同步机制

- 同步记录：`docs/merges/2026-09-14-upstream-main.md`、`2026-09-17`、`2026-09-28`。
- 流程：拉取 upstream → 合并 → 逐项内容审计（记录 identical patches 与 adjusted counterparts 数量）→ 保留本地集成工作 → 通过 ancestry merge 记录历史。
- 最近一次：`df6167e7d Merge upstream multica-ai/multica (9b1ab4f9) into fork`。
- **教训**：同步后必须手动跑一次全量 CI。上游 `changes` 过滤会让未触及的包跳过测试，fork 专属回归（例如 unread 视图链路缺字段、locale parity）只有全量运行才暴露。

---

## 7. 质量保障

### 7.1 单测规模（fork 新增模块）

| 模块 | Go 文件 | 测试文件 |
| --- | --- | --- |
| `daemon` | 585 | 320 |
| `mirror` | 75 | 31 |
| `runtimeproc` | 33 | 12 |
| `daemonws` | 22 | 9 |
| `jevmodels` | 21 | 8 |
| `vscreen` | 14（+3 子包） | 6 |
| `applicationgateway` | 12 | 5 |
| `communications` | 13 | 4 |
| `application` | 15 | 3 |

前端：`views` 分片测试 629 通过 / 1 skip；`core` 249 通过；桌面端 typecheck + 打包校验通过。

### 7.2 2026-11-07 CI 修复批次

全量 dispatch 暴露并修复了三个长期被 `changes` 门控掩盖的问题：

| 提交 | 问题 | 修复 |
| --- | --- | --- |
| `3aea4aa2f` | unread 视图链路在 fork 中丢失 `unreadFilter`，请求不带 `unread_issue_ids`，侧栏不刷新 | 从上游恢复成套文件并补齐 store 字段、fixtures |
| `123bc0a40` | `packages/ui` 直接 import `lucide-react`，72 处 `no-restricted-imports` | 迁移到 `@multica/ui/icons`，字形集中到 `icons/drawings/drawings.mjs` 生成 |
| `3fbc87615` | 移动端 zh-Hant 缺多组 locale 键；`Date.now()` 违反 react-hooks/purity | 补齐六语言 parity；改用 `useState` 初始化器 |
| `f7c62c284` | 单个僵尸 run 阻塞整个发布队列 | 并发组按 tag 隔离 + 支持 dispatch 重跑 |
| `340ef9071` | Windows 无法 unlink 仍打开的临时文件，WeCom 流式解密全挂；`/views` 路由段未登记 | unlink 失败退化为 Close 时删除；补 `reserved_slugs.json` |
| `ed807eac4` | `attachment_text_preview.json` 在 fork 中丢失导致漂移检查失败；JEV 字段错误退化成泛化 `invalid_request` | 从上游恢复生成源；`prepareCompletionInput` 增加按字段探测兜底 |

修复后 `main` 上 push 触发与全量 dispatch 双绿。

---

## 8. 已知边界与技术债

1. **Linux 不支持镜像交互**，首版也不支持镜像采集；入口需明确说明，不做静默降级。
2. **镜像仅支持主显示器**，不支持多源选择、窗口选择、录制回放、音频采集。
3. **服务端永不落像素与输入**：任何「服务端转存/回放」需求都与当前架构冲突。
4. **桌面版本线与根版本线解耦**：发版时必须同时 bump `apps/desktop/package.json`，否则 `fork-desktop.yml` 的版本校验会失败。
5. **macOS GitHub 托管 runner 容量不稳定**：曾连续多次出现 `job was not acquired by Runner`，非代码问题；必要时本地打包后手动上传。
6. **fork 与上游差异极大**（648 提交 / 3,540 文件），每次上游同步的冲突成本高，必须依赖 `docs/merges` 的内容审计流程。
7. **发布资产依赖签名密钥**：未配置 `MAC_CSC_LINK` / `WIN_CSC_LINK` 等 secrets 时，包可构建但无受信任签名，macOS 自动更新安装会受 Gatekeeper 限制。

---

## 附录 A：fork 独有提交类型分布

| 类型 | 数量 |
| --- | --- |
| `fix` | 312 |
| `feat` | 149 |
| `test` | 86 |
| `merge` | 50 |
| `docs` | 15 |
| `ci` | 8 |
| `chore` | 5 |
| `wip` | 2 |
| `style` / `perf` / `chat` | 各 1 |
| 非规范前缀（`MUL-xxxx` 等） | 18 |

## 附录 B：相对上游的目录改动 Top

| 目录 | 文件数 | 新增行 | 删除行 |
| --- | --- | --- | --- |
| `server/internal` | 1,897 | 189,532 | 74,297 |
| `packages/views` | 419 | 81,790 | 10,770 |
| `server/pkg` | 322 | 22,560 | 24,345 |
| `server/migrations` | 278 | 808 | 674 |
| `packages/core` | 190 | 11,686 | 122 |
| `server/cmd` | 122 | 5,220 | 7,598 |
| `apps/desktop` | 108 | 7,226 | 75 |
| `apps/docs` | 40 | 729 | 313 |
| `apps/mobile` | 28 | 501 | 20 |
| `.github` | 5 | 395 | 2 |

## 附录 C：相关文档索引

| 文档 | 内容 |
| --- | --- |
| `docs/prd/runtime-mirroring.md` | 运行时镜像 PRD（目标、非目标、场景） |
| `docs/prd/runtime-mirror-control.md` | 镜像交互 PRD（人工操作、智能体代操作、FCFS 仲裁） |
| `docs/jev-model-sources.md` | Jev 三来源配置方案 |
| `LOCAL_MR_ACCEPTANCE.md` / `LOCAL_MR_IMPLEMENTATION.md` | 本地 MR 验收口径与实现状态 |
| `SELF_HOSTING_FORK.md` | fork 自托管部署 |
| `.github/RELEASING.md` | 发布 runbook（含 fork 与上游两套） |
| `docs/merges/*.md` | 上游同步内容审计记录 |
| `DESIGN.md` | 设计系统约束（颜色、字号、间距、chat 布局） |
| `docs/agent-communications.md` | 智能体通信 |
| `packages/views/runtimes/DESIGN.md` | 镜像前端设计说明 |

## 附录 D：复现统计的命令

```bash
# fork 独有提交
git log --oneline HEAD --not 9b1ab4f9e | wc -l

# 净差异
git diff --shortstat 9b1ab4f9e HEAD

# 目录改动
git diff --numstat 9b1ab4f9e HEAD

# 提交类型分布
git log --format='%s' HEAD --not 9b1ab4f9e \
  | sed -E 's/^([a-z]+)(\(([^)]+)\))?!?:.*/\1/' | sort | uniq -c | sort -nr

# 新增文件分布
git diff --name-status 9b1ab4f9e HEAD | awk '$1=="A"'
```
