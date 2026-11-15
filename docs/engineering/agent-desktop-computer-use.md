# 以 agent-desktop 统一桌面操作

- 状态：仓库实现完成，镜像人工接管经 agent-desktop 执行；生产迁移与平台验收待完成
- 生产数据：`runtime_vscreen_intervention` 删除迁移已加入仓库，但尚未在生产执行或取得产品授权
- 固定版本：`agent-desktop v1.3.1`
- 日期：2026-11-15

本文记录最终架构与覆盖边界。仓库不保留 feature flag、双写、运行时 fallback 或旧 GUI 控制适配层。

## 1. 决策

Multica 的桌面输入只保留一个执行引擎：`agent-desktop`。运行时镜像既支持只读观看，也支持经显式授权的人工接管；镜像页自身不实现输入，接管输入仍全部交给 `agent-desktop`。

最终移除 Multica 自研的 GUI 操作能力，包括：

- `multica-vscreen` MCP 工具；
- `vscreen_ui_tars` 视觉兜底；
- Multica 自研虚拟屏幕；
- 自研窗口和辅助功能树操作；
- 自研全局鼠标、键盘注入；
- 浏览器运行时镜像中绕过 agent-desktop 的直接控制；
- GUI 人工接管后恢复智能体的专用链路。

运行时镜像继续保留屏幕观察。用户也可以请求人工控制；runtime owner/admin 批准后，daemon 建立受 lease 约束的 `agent-desktop` session，并把镜像页的指针、键盘与剪贴板命令转交该 session。浏览器不协商输入 DataChannel，也不恢复任何 Multica 自研注入路径。

系统最终只有一种方式产生桌面输入：`agent-desktop`。

### 1.1 范围

本方案覆盖 Multica 生产 runtime 中的智能体桌面操作：

- 智能体查看和操作 macOS、Windows、Linux 桌面应用；
- 任务级 MCP 注入、并发控制、审批和清理；
- Multica Desktop 与 CLI 的 `agent-desktop` 分发和升级；
- 保留运行时屏幕镜像，并增加经 lease 和审批约束的人工接管。

以下内容不属于“第二套 GUI 操作”，不受“只能使用 agent-desktop”约束：

- Playwright 等产品自动化测试；
- 用户本人通过操作系统直接操作电脑；
- 只读屏幕采集和 WebRTC 编码。它们负责观察，不产生桌面输入；
- 智能体通过 shell 完成普通命令行任务。智能体不得用 `osascript`、AppleScript、PowerShell、`xdotool`、`ydotool`、`pyautogui`、UI-TARS 或其他脚本绕过 `agent-desktop` 执行 GUI 操作。

### 1.2 硬约束

1. 不保留 Multica 自研 GUI 动作实现作为备用路径。
2. 不通过 feature flag 在运行时二选一。
3. 不在 `agent-desktop` 失败后自动启用旧工具或原始坐标注入。
4. 镜像页的输入只能由已批准的控制 lease 发送，并由 agent-desktop 执行；未授权时保持只读。
5. 旧客户端连接新服务端、新客户端连接旧服务端时，可以安全降级为只读；降级不得重新启用旧控制能力。
6. 数据迁移和协议收窄必须在同一个完整交付中完成，不能长期保留两套 contract。

## 2. 替换前基线

决策前，daemon 会为符合条件的任务启动 `multica-vscreen` MCP，并向智能体注入 `vscreen_*` 工具。该基线主要包括：

- `server/internal/daemon/` 中的 vScreen MCP、执行、报告、人工接管和输入仲裁；
- `server/internal/vscreen/` 中的虚拟显示、应用发现、AX/Win32 操作、全局输入和 lease；
- `server/pkg/protocol/` 中的 action、input、control、intervention 和 receipt；
- `server/internal/handler/` 与 WebSocket 中的 vScreen 干预和 mirror control API；
- `packages/core/`、`packages/views/`、`apps/desktop/` 中的控制状态、授权弹窗和镜像输入；
- `runtime_vscreen_intervention` 及其查询、索引和清理逻辑。

上述旧输入路径在实施完成后全部删除。保留屏幕采集、编码、镜像授权和观看链路，并新增只调用 agent-desktop 的人工接管控制面。

`agent-desktop v1.3.1` 已提供：

- macOS AX API；
- Windows UI Automation；
- Linux AT-SPI；
- CLI 和 stdio MCP；
- 快照、语义元素引用、点击、输入、滚动、拖拽；
- 应用、窗口、通知、剪贴板、等待和会话；
- 无截图和逐像素匹配的 observe-act-observe 循环。

对应能力说明：[agent-desktop README](https://github.com/vercel-labs/agent-desktop/blob/v1.3.1/README.md)。

替换后：

| 能力 | 当前 | 目标 |
| --- | --- | --- |
| 桌面观察 | Multica vScreen AX / PNG / UI-TARS | `agent-desktop` |
| 元素定位 | Multica snapshot revision | `agent-desktop` refs |
| 点击和输入 | Multica action protocol | `agent-desktop` commands |
| 窗口管理 | Multica app control | `agent-desktop` |
| 虚拟屏幕 | Multica native virtual display | 删除 |
| 智能体 GUI 租约 | Multica vScreen lease | 通用桌面任务互斥 |
| 浏览器远程控制 | Multica global input | 删除；改由批准的 agent-desktop session 执行 |
| 屏幕镜像 | Multica WebRTC | 保留视频观看与人工接管入口 |

## 3. 目标架构

```text
智能体运行时
    │
    │ stdio MCP
    ▼
multica internal-agent-desktop-bridge
    │  任务会话、互斥、审批和诊断，不实现 GUI 动作
    ▼
agent-desktop mcp
    │
    ├─ macOS: AX API
    ├─ Windows: UI Automation
    └─ Linux: AT-SPI

运行时镜像
    物理屏幕 ──采集──> WebRTC ──> 浏览器观看
                                  │
                                  └─ 批准的人工输入 ─> daemon ─> agent-desktop
```

`internal-agent-desktop-bridge` 是控制平面，不是第二套 GUI 实现。它负责：

- 解析固定版本的 `agent-desktop`；
- 创建和结束任务级 session；
- 获取单运行时桌面操作 lease；
- 检查系统权限；
- 拦截并审批高风险工具调用；
- 在任务取消、失败和完成时清理子进程。

人工接管使用同一个 runtime desktop lease，但由 daemon 创建独立的 `agent-desktop` session。镜像页只发送结构化控制请求；daemon 将归一化画布坐标映射到显示器逻辑坐标，再调用 agent-desktop 的 headed 命令。

点击、输入、窗口选择和辅助功能树读取全部由 `agent-desktop` 完成。

## 4. agent-desktop 的交付与版本管理

### 4.1 固定版本

固定 `agent-desktop v1.3.1`。不依赖用户的全局 npm 安装，也不依赖用户 `PATH` 中的随机版本。

Multica 分发 release archive 中的原生产物，并生成自己的版本文件和校验清单：

```text
~/.multica/tools/agent-desktop/1.3.1/
  agent-desktop
  agent-desktop-macos-helper       # 仅 macOS
  agent-desktop-linux-clipboard-owner  # 仅 Linux
  VERSION
  SHA256SUMS
  LICENSE
```

Windows 的主程序名为 `agent-desktop.exe`，release archive 不包含额外 helper。

固定 release 资产：

| 平台 | Release asset | 解包产物 | Archive SHA-256 |
| --- | --- | --- | --- |
| macOS arm64 | `agent-desktop-v1.3.1-aarch64-apple-darwin.tar.gz` | `agent-desktop`, `agent-desktop-macos-helper` | `9b7b7cfc74892eab46c2c85d6cb7dc4863bf8dab0a222cfcfb8ae4604c69c86c` |
| macOS x64 | `agent-desktop-v1.3.1-x86_64-apple-darwin.tar.gz` | `agent-desktop`, `agent-desktop-macos-helper` | `469da10cfb68928c58caff88b92b30526e0ca895ac06bc86b7f533b1d9b309b3` |
| Linux arm64 | `agent-desktop-v1.3.1-aarch64-unknown-linux-gnu.tar.gz` | `agent-desktop`, `agent-desktop-linux-clipboard-owner` | `438990a703fbeb19775596c341c3701435e80adbfa1b2f64b82ff17567d5573b` |
| Linux x64 | `agent-desktop-v1.3.1-x86_64-unknown-linux-gnu.tar.gz` | `agent-desktop`, `agent-desktop-linux-clipboard-owner` | `ca77b6b75034eeb12e537dd8c0a4f2a3424119253f38b7e6bd79ba0d2fef1264` |
| Windows arm64 | `agent-desktop-v1.3.1-aarch64-pc-windows-msvc.tar.gz` | `agent-desktop.exe` | `805f1f61890f9983fceaf38cda94a37a049c379435d65607db5cedd22c8ef97b` |
| Windows x64 | `agent-desktop-v1.3.1-x86_64-pc-windows-msvc.tar.gz` | `agent-desktop.exe` | `6ee4b1e078f4e78792e79f16e32e4a78b9919464aa3c55629b7e27ed53a08c13` |

要求和边界：

- Desktop 安装包和 Multica CLI release 包携带相同版本的平台资产。
- 安装时先校验 archive SHA-256，再解包；写入 `SHA256SUMS` 后，每次启动校验主程序及同目录 helper。
- 安装到临时目录并原子替换，不留下只写入一半的可执行目录。
- 启动前执行版本检查，版本不一致时明确失败。
- 不自动跟随 `latest`。
- 随 Multica release 升级，避免独立升级造成协议漂移。
- 分发上游 Apache-2.0 `LICENSE`，记录版本、release tag 和源码地址。上游 release 根目录没有单独的 `NOTICE`，不虚构该文件。
- 不允许在失败后回退到 Multica 自研 GUI 工具。

### 4.2 解析顺序

只允许一个受控覆盖项用于开发和高级部署：

1. 显式配置的 `MULTICA_AGENT_DESKTOP_BIN` 绝对路径；
2. 默认固定路径 `~/.multica/tools/agent-desktop/1.3.1/agent-desktop`；
3. 预检失败。

不接受任意找到的 `PATH` 版本。覆盖项不是第二实现，只服务于测试或受管部署；它仍必须通过精确版本检查，受管部署还必须校验配置的 SHA-256。

## 5. 任务级 MCP 接入

### 5.1 注入条件

只有同时满足以下条件的任务注入 `agent-desktop`：

- task 属于当前本地 runtime；
- provider 支持 stdio MCP；
- task 携带经过服务端鉴权的 mirror source binding，即用户从可见的 runtime 镜像上下文发起任务；
- runtime 处于用户图形会话，且预检可用；
- 当前 runtime 没有其他桌面任务持有 lease。

mirror source binding 只表示任务绑定了用户正在观看的 runtime，不授予镜像页直接输入能力，也不作为 `agent-desktop` 的坐标或窗口范围。`agent-desktop` 仍通过应用和 window 的辅助功能树重新观察。

不再使用 `VscreenContinuation`、接管后恢复、intervention 或“返回智能体”字段作为注入条件。缺少 binding 的普通 coding task 不注入桌面工具。带 binding 但 provider、图形会话或预检不满足时，任务必须明确失败，不能继续运行后在任务中临时选择另一套 GUI 工具。

Daemon 在注入前校验 provider 能力和 runtime 状态。Bridge 接收 daemon 给出的 task/runtime scope；它不自行从 `PATH` 查找程序，也不为其他 task 启动 session。

### 5.2 MCP 配置

Daemon 为桌面任务注入：

```json
{
  "mcpServers": {
    "agent-desktop": {
      "command": "/path/to/multica",
      "args": [
        "internal-agent-desktop-bridge",
        "--task-id",
        "<task-id>",
        "--runtime-id",
        "<runtime-id>",
        "--agent-desktop",
        "/Users/<user>/.multica/tools/agent-desktop/1.3.1/agent-desktop"
      ]
    }
  }
}
```

如果 runtime 显式开启物理输入，bridge 参数增加 `--headed`。默认不添加，agent-desktop 通过 AX/UIA/AT-SPI 的 ref 操作，不移动全局指针或抢占焦点。

`agent-desktop` 是保留 MCP 名称。用户配置或其他 runtime MCP 已使用该名称时，任务预检失败，不覆盖、不合并为第二个别名。

### 5.3 Bridge 生命周期

1. 读取并验证绝对路径的固定版本二进制；
2. 获取 runtime 级桌面操作 lease；
3. 执行 `agent-desktop version`；
4. 执行 `agent-desktop status` 和 `agent-desktop permissions` 获取 readiness，不自动打开授权引导；
5. 执行 `agent-desktop session start --no-trace --name multica-<task-id>`；
6. 启动 `agent-desktop mcp --session <session-id>`；
7. 双向转发 MCP JSON-RPC，对高风险 `tools/call` 执行审批；
8. 在完成、失败和取消时关闭 stdin、终止子进程并执行 `agent-desktop session end <session-id>`。

Bridge 不实现 click、type、snapshot、window、AX 或 AT-SPI。它只做程序解析、生命周期、互斥、审批、诊断和结果透传。

智能体看到的工具名保持 `agent-desktop` 原生形式：

- `desktop_snapshot`
- `desktop_find`
- `desktop_click`
- `desktop_type`
- `desktop_set_value`
- `desktop_press`
- `desktop_scroll`
- `desktop_launch`
- `desktop_wait`
- `desktop_screenshot`
- 其他 agent-desktop 命令

不再转换成：

- `vscreen_acquire`
- `vscreen_observe`
- `vscreen_click`
- `vscreen_type`
- `vscreen_ui_tars`

智能体的固定操作循环：

```text
snapshot → action → snapshot/find 验证
```

发生 `STALE_REF` 或 `AMBIGUOUS_TARGET` 时重新 snapshot，不重复不确定动作。只有 `error.disposition.retry` 为 `safe` 时才能重试。

系统提示明确禁止智能体使用 shell、AppleScript、PowerShell、`xdotool`、UI-TARS 或其他桌面自动化作为 GUI fallback。普通非 GUI shell 任务不受此限制。

## 6. 任务隔离和并发

### 6.1 每任务 session

- 每个任务拥有独立的 agent-desktop session。
- snapshot 和 ref 不跨任务复用。
- 任务结束后释放 session 子进程。
- 任务取消时向正在运行的 `tools/call` 发送取消并终止子进程。

### 6.2 单运行时桌面 lease

同一 runtime 同时只允许一个桌面操作任务。

规则：

- 桌面任务开始前获取 lease。
- 第二个桌面任务在启动阶段收到明确的“桌面正在被其他运行使用”错误，不静默降级为无桌面工具的任务。
- lease 不执行任何 GUI 动作，只负责串行化。
- 任务完成、失败或取消后释放 lease。
- daemon 启动时清理上一次异常退出遗留的 session；清理失败时桌面能力保持不可用，并报告明确诊断。

### 6.3 仅本地图形会话

只有运行在用户图形会话中的本地 runtime 可以使用桌面工具。

以下情况不注入工具：

- 云端 runtime；
- 远程容器 runtime；
- 无法访问图形会话的 daemon；
- Linux SSH、TTY 或无 AT-SPI bus 的进程；
- `agent-desktop status` 报告没有可用 surface。

失败时显示具体原因，不回退到旧的 Multica GUI 工具。

## 7. 权限模型

### 7.1 系统权限

任务开始前执行 `agent-desktop permissions`，不自动触发系统授权提示。

平台差异：

- macOS：需要 Accessibility；截图需要 Screen Recording。
- Windows：受进程完整性和 Smart App Control 影响。
- Linux：需要同一图形会话、AT-SPI bus，以及对应 X11 或 Wayland 能力。

Runtime 详情页展示：

- 当前平台；
- `agent-desktop` 版本；
- Accessibility、Screen Recording、Automation 状态；
- 可用 surface 数量；
- 修复入口和不可用原因。

### 7.2 高风险工具审批

Bridge 可以拒绝转发一个工具调用，但不能自行实现该动作。

默认需要成员审批：

- `desktop_clipboard_*`
- `desktop_close_app`
- `desktop_permissions` 的非只读模式
- `desktop_*notification*`
- `desktop_open_system_surface`
- `desktop_trace*`
- `desktop_sandbox_*`
- `desktop_key_down`
- `desktop_key_up`
- `desktop_mouse_down`
- `desktop_mouse_up`
- `desktop_mouse_move`
- `desktop_mouse_click`
- `desktop_mouse_wheel`

审批提示展示具体工具、目标 app/window（如可解析）、可能影响和本次调用是否属于物理输入。审批只对应一次 `tools/call`，不把“本次允许”缓存成永久权限。

`desktop_batch` 不能被当作低风险工具直接放行。Bridge 必须解析 batch 中的每个子命令，并对所有子命令应用同一策略；任一子命令需要审批时，整个 batch 作为一个审批请求展示，任一子命令被拒绝时整个 batch 不转发。

审批复用现有 run-scoped human-request 流程，不新增桌面控制状态机：

1. Bridge 使用当前 task 的 CLI 身份创建 `confirmation` 请求，使用每次 tool call 的稳定 key、具体 tool 名称、impact 和脱敏 details；
2. 高风险请求固定 `card_only`，等待指定成员对 exact revision 作出明确响应；
3. `approve` 后 bridge 转发原 MCP `tools/call`；`reject`、过期、取消或 task 被取消时返回 MCP error；
4. 审批请求本身不包含可执行按钮、snapshot、clipboard 内容或凭据；
5. 超时只产生失败，不自动批准，也不切换到旧 GUI 实现。

如果当前运行环境无法创建或读取 human-request，高风险工具直接不可用；低风险观察和 ref 操作仍可继续。

物理指针和键盘模式由 runtime 级开关控制。开启后 agent-desktop 使用 `--headed`，默认关闭。开关本身不代替逐次工具审批；raw pointer、held key/button、系统面和跨应用副作用仍按上面的规则处理。

审批通过后仍由 `agent-desktop` 执行。Multica 不解析或重写具体 GUI 动作。

### 7.3 数据保护

- 默认不启用 agent-desktop trace。
- 默认不保存动作前后截图。
- snapshot、clipboard 和 screenshot 不写入服务端日志。
- 不把 agent-desktop trace HTML 或 refmap 上传到服务端。
- screenshot 和桌面文本按普通智能体输出处理，继续遵守附件和 transcript 权限。
- 服务端不存储桌面画面。
- Bridge 只记录 tool 名称、耗时、错误码和清理结果；不记录参数中的文本、snapshot 树、clipboard 内容或画面。

## 8. 删除 Multica GUI 执行链路

### 8.1 Daemon

删除：

- `server/internal/daemon/vscreen_app.go`
- `server/internal/daemon/vscreen_execution.go`
- `server/internal/daemon/vscreen_execution_transcript.go`
- `server/internal/daemon/vscreen_mcp.go`
- `server/internal/daemon/vscreen_uitars.go`
- `server/internal/daemon/vscreen_physical_*`
- `server/internal/daemon/vscreen_intervention*`
- `server/internal/daemon/vscreen_report*`
- `server/internal/daemon/vscreen_smoke_*`
- `server/internal/daemon/mirror_process_gui.go`
- 上述文件对应的测试、fixture 和测试数据

Daemon 不再包含：

- `VscreenInputHandler`
- `VscreenTakeoverHandler`
- 智能体控制租约
- UI-TARS 模型和凭据配置
- 自研 app/window 操作
- 自研输入仲裁

### 8.2 internal/vscreen

删除动作和窗口控制相关包：

- `server/internal/vscreen/action.go`
- `server/internal/vscreen/driver.go`
- `server/internal/vscreen/lease.go`
- `server/internal/vscreen/manager*`
- `server/internal/vscreen/appclaim/`
- `server/internal/vscreen/hostclient/app*`
- `server/internal/vscreen/native/app_*`
- `server/internal/vscreen/native/appcontrol/`
- `server/internal/vscreen/native/globalinput/`
- `server/internal/vscreen/smokefixture/`

删除虚拟显示创建、窗口接管、元素树、输入注入和 app claim。

### 8.3 镜像观看与人工接管

从现有采集中抽出只读能力：

```text
server/internal/mirror/nativecapture/
```

保留：

- 物理主屏和系统屏幕枚举；
- Screen Recording 权限探测；
- macOS 和 Windows 屏幕采集；
- WebRTC H264 视频；
- 多观看者共享捕屏源；
- 镜像授权；
- 观看开始和结束通知。

移除 Multica 自研路径：

- virtual 镜像源；
- 浏览器输入 DataChannel；
- `screen-control-v1`；
- 旧 `mirror_control*` 与 vScreen control grant；
- 镜像页直接注入鼠标键盘；
- 接管、窗口选择和返回智能体；
- `runtime_vscreen_intervention`。

保留并新增一个独立的人工接管控制面：`/agent-desktop/control`。它复用 viewer 权限，要求 runtime owner/admin 批准非 owner 请求，维护 2 分钟空闲、30 分钟最长 lease，并把动作转给 daemon 中持有同一 desktop lease 的 agent-desktop `ControlSession`。该能力不是 WebRTC 输入通道，也不是旧的 vScreen 控制实现。

## 9. 协议清理

删除旧实现：

- `vscreen_action*`
- `vscreen_input*`
- `vscreen_control*`
- `vscreen_control_grant*`
- `vscreen_intervention*`
- `vscreen_receipt`
- `vscreen_query`
- `vscreen_command`
- `vscreen_result`
- `vscreen_intervention_ack`
- `runtime_mirror:control`
- 旧 `mirror:control-grant`
- 旧 `mirror:control-revoke`
- `MirrorControlStatePayload`
- `MirrorControlGrant`

保留镜像观看，并新增经批准的 agent-desktop 控制事件：

- screen mirror SDP；
- `MirrorViewerGrant` 和 viewer credential；
- physical/system source；
- 镜像观看权限；
- 观看者状态通知；
- `agent-desktop:control-*-query/result`。

`MirrorViewerGrant` 只允许观看。它不能被解释为控制能力，也不能换取任何 input DataChannel 权限。

新协议不再接受 `MirrorSourceVirtual`。服务端收到 virtual source 请求时返回 `source_unavailable`；旧服务端如果返回 virtual source，新客户端将其解析为不可用来源并保持只读，不调用旧 remote-control API。

WebRTC offer/answer 不协商输入 DataChannel。daemon 和客户端均拒绝 `screen-control-v1`；未知 capability 只用于诊断，不启用旧路径。

## 10. API 和 UI 清理

删除以下旧控制接口：

- `/api/runtimes/{runtimeId}/vscreen`；
- `/api/runtimes/{runtimeId}/vscreen/interventions`；
- `/api/runtimes/{runtimeId}/vscreen/commands/*`；
- `/api/daemon/runtimes/{runtimeId}/vscreen/interventions`；
- `/api/tasks/{taskId}/vscreen/interventions/{id}` 及 `continue`、`cancel`；
- `/api/runtimes/{runtimeId}/mirror/control-state`；
- `/api/runtimes/{runtimeId}/mirror/control-grants` 及 `renew`、`revoke`；
- Runtime GUI 控制状态；
- 旧 vScreen 接管按钮；
- 窗口选择；
- 远程控制授权 Prompt；
- 控制开始和结束通知；
- 所有 `vscreen.*` 控制文案和翻译。

Runtime 能力声明从：

```text
virtual-screen-v1
screen-control-v1
mirror-viewer-grant-v1
```

收窄为：

```text
screen-mirror-video-v2
mirror-viewer-grant-v1
```

`agent-desktop-v1` 是 daemon/MCP readiness capability，不进入浏览器镜像 capability 集合。

镜像页在只读状态显示：

- runtime 名称；
- 连接状态；
- 屏幕来源；
- 实时画面；
- 权限和连接错误。

镜像页还提供经批准的 Request/Approve/Deny/Stop 控制状态，以及键盘捕获、剪贴板、文本输入、绝对/相对指针和触屏手势。所有控制请求都转到 agent-desktop 控制接口，而不是旧 mirror control API。

### 10.1 混合版本切流

最后交付时不保留选择逻辑，但发布窗口必须防止旧 daemon 继续运行 vScreen：

- 新 daemon 只有在固定 `agent-desktop` 可执行、版本正确且图形会话可探测时声明 `agent-desktop-v1`。
- 服务端只向声明 `agent-desktop-v1` 的 daemon 派发带 mirror binding 的桌面任务。
- 未升级 daemon 不接收新的桌面任务，因此旧 vScreen 不会在升级窗口继续产生桌面输入。
- 新服务端停止签发 control grant，旧客户端即使保留控制 UI 也只能得到 404 或 `source_unavailable`。
- 新客户端删除控制调用；连接旧服务端时仍只观看 physical/system source，绝不回退到旧控制实现。
- 所有 runtime 升级完成后，删除临时版本切流 gate。最终仓库只理解 `agent-desktop-v1`。

## 11. 数据库迁移

新增迁移删除：

- `runtime_vscreen_intervention`；
- 该表的查询文件、生成代码和 workspace delete 清理依赖；
- migration runner 中仅为该表保留的索引 cleanup 注册。

这是不可逆数据删除。迁移只删除 vScreen 接管记录，不删除任务、项目、任务实体、智能体或运行时数据。如果产品要求保留历史，只能在删除前另行归档；不得让归档表继续承担运行时控制状态或形成第二套数据源。

迁移交付顺序：

1. 先发布停止写 intervention 的服务端；旧客户端请求返回 404。
2. 确认没有新写入和未完成 continuation。
3. rebase 到最新 `origin/main`，使用当前最高 migration prefix 加一创建新迁移。
4. up migration 使用 `DROP TABLE IF EXISTS runtime_vscreen_intervention`；相关索引随表删除，不再创建新索引。
5. down migration 重建空表结构以支持 migration 回滚，但已删除的行无法恢复；生产发布说明必须明确这一点。
6. 运行 `make sqlc`，删除不再生成的 SQL 和 generated 类型。
7. 更新 workspace delete manifest、handler 和 migration 测试中对该表的引用。

实施时必须遵守仓库迁移规则：

- 不新增外键和级联删除；
- 不因为 down migration 重建表而把运行时代码继续指向该表；
- migration 双向执行通过；
- 迁移测试覆盖表存在、删除幂等、workspace cleanup 不再查询该表；
- 先停止新写入，再执行删除。

### 11.1 发布硬门槛

以下条件全部满足后才能执行删除迁移：

- 新服务端已拒绝 control 和 intervention 请求；
- 所有可调度桌面任务的 daemon 已能声明 `agent-desktop-v1`；
- 没有任务仍依赖 `VscreenContinuation`；
- 数据库备份、迁移 dry-run 和 down migration 已在临时数据库验证；
- Desktop、Web 和 CLI 的最低兼容版本已在发布说明中写明。

## 12. 实施拆分

最终代码不保留 feature flag、双写或运行时 fallback。提交可以分步，但必须一起交付。

### Commit 1：接入 agent-desktop

- 固定 `v1.3.1` 二进制、平台 helper、版本和 SHA-256 manifest；
- 新增 resolver、哈希和版本检查；
- 新增 `internal-agent-desktop-bridge`；
- 增加 MCP JSON-RPC 转发、高风险审批和 batch 子命令检查；
- 增加 session、桌面 lease、取消和异常退出清理；
- 增加权限、surface 和 readiness 探测。

### Commit 2：切换任务注入

- Daemon 只注入 `agent-desktop` MCP；
- 删除 `multica-vscreen` 注入；
- 增加 `agent-desktop-v1` daemon capability，并停止向旧 daemon 派发桌面任务；
- 替换 GUI prompt 和智能体指令；
- 删除 UI-TARS 调用。

### Commit 3：删除执行链路

- 删除 daemon vScreen execution、MCP、app control 和 intervention；
- 删除 `internal/vscreen` GUI 包；
- 删除自研输入和窗口协议；
- 删除 `apps/desktop/scripts/vscreen-*` 中已经失去验证目标的执行 smoke，保留或改写只读镜像测试；
- 将屏幕采集移到只读 mirror capture。

### Commit 4：镜像与人工接管 UI

- 删除旧远程控制、旧接管和窗口选择；
- 将镜像页改为画布优先、默认只读的会话壳层；
- 新增经审批且只调用 agent-desktop 的人工接管控制面；
- 增加 Source overlay、缩放/平移、画质协商、诊断、输入 Dock 和 Inspector；
- 删除旧 API、schema、事件、文案、翻译和测试，新增控制协议测试。

### Commit 5：数据库和分发

- 删除 intervention 表；
- `scripts/install.sh`、`scripts/install.ps1`、`apps/desktop/src/main/cli-bootstrap.ts` 加入 agent-desktop；
- Desktop 与 CLI release workflows 加入六个平台 archive、helper 和 Apache-2.0 LICENSE；
- 更新安装、升级和校验流程；
- 更新工程文档和故障诊断。

提交顺序只用于 review，不得分批长期上线。代码 merge 时已经只有一条桌面输入路径；发布时先准备资产和 capability，再一次性切换服务端、daemon、Web 和 Desktop 的最小兼容版本。

## 13. 测试矩阵

### 13.1 Go

- 固定版本二进制被正确解析。
- binary 缺失、版本错误或哈希错误时明确失败。
- 任务 MCP 配置只包含 `agent-desktop`。
- 配置中不再出现 `multica-vscreen`。
- 每任务 session 在成功、失败、取消后均被清理。
- 同 runtime 第二个桌面任务被拒绝。
- 旧 daemon 未声明 `agent-desktop-v1` 时不会收到桌面任务。
- 未审批的高风险工具调用不会转发。
- 审批通过后调用转发给 agent-desktop。
- `desktop_batch` 中任一需要审批的子命令不会被绕过。
- trace 和 screenshot 默认关闭。
- 镜像 SDP 和 viewer grant 正常工作。
- 镜像协议不接受任何输入消息。
- 新服务端到旧客户端、新客户端到旧服务端都不会重新启用 control。

### 13.2 前端

- 镜像页默认只读，批准后进入持续可见的控制模式。
- 不存在旧远程控制按钮、旧接管弹窗和窗口选择。
- 人工控制状态覆盖 view-only、等待批准、批准中、本人控制、他人控制、已结束。
- 未批准或 lease 过期时，镜像页不能发送输入。
- agent-desktop 权限和 readiness 状态可操作。
- 旧服务端返回 virtual source 时安全降级。
- 所有支持语言同步删除控制文案。

### 13.3 安装与发布

- 三个 OS、两种架构的 archive SHA-256 与本文表格一致。
- macOS 同时安装 `agent-desktop-macos-helper`；Linux 同时安装 `agent-desktop-linux-clipboard-owner`。
- 主程序与 helper 权限正确，路径和 `VERSION` 一致。
- 原子升级失败时保留上一份完整目录，不留下半成品。
- Desktop 和 CLI 启动 daemon 前都解析同一固定版本。
- 缺少资产、版本不符或 helper 缺失时，桌面任务明确失败。

### 13.4 macOS

- 使用 Finder 或 Notes 完成 snapshot、click、type、再次 snapshot。
- Accessibility 被拒绝时显示修复入口。
- Screen Recording 缺失时截图返回明确错误。
- 任务取消后 agent-desktop 子进程退出。

### 13.5 Windows

- 使用记事本完成 observe-act-observe。
- 覆盖未签名 binary、Smart App Control 和高权限目标诊断。
- 目标应用以管理员运行时，普通完整性进程不会假装成功。

### 13.6 Linux

- Ubuntu 24.04 GNOME 下通过 AT-SPI 完成 observe-act-observe。
- Wayland 不支持的动作返回 `ACTION_NOT_SUPPORTED`。
- 不尝试退回原始坐标注入或 Multica 自研实现。
- SSH、TTY 和没有 AT-SPI bus 时明确不可用。

### 13.7 回归

- snapshot、clipboard 和私有窗口文本不进入日志。
- 不确定动作不自动重试。
- 智能体被取消后没有残留 session 或子进程。
- 同一 runtime 不出现两个智能体同时操作桌面。
- 镜像可以观看；只有有效批准 lease 才能经 agent-desktop 产生输入。

## 14. 验收标准

1. 仓库运行时代码中不存在 `multica-vscreen` 任务工具。
2. 不存在 `vscreen_ui_tars`。
3. 不存在 Multica 自研鼠标、键盘、窗口和 AX 操作实现。
4. 浏览器镜像未获批准时不能控制桌面；获批准时输入只经 agent-desktop。
5. 智能体获得的所有 GUI 工具均来自 `agent-desktop`。
6. agent-desktop 缺失或权限不足时明确失败，不回退旧实现。
7. 整个系统只有一种桌面输入路径：`agent-desktop`。
8. 屏幕镜像继续工作，并支持审批、lease、停止和过期收回的人工接管。
9. macOS、Windows、Linux 分别完成真实的 observe-act-observe 验收。
10. 安装、升级、卸载和版本校验覆盖三个平台。
11. 未升级 daemon 不再获得桌面任务，旧 control API 和事件不可用。
12. snapshot、clipboard、screenshot 内容和 tool 参数不进入服务端日志。

## 15. 风险与明确取舍

### 15.1 失去 Multica 虚拟屏幕隔离

`agent-desktop` 操作用户真实桌面，不再为每个智能体创建后台虚拟显示。

结果：

- 用户可以看到智能体正在操作什么；
- 不同智能体必须在 runtime 级串行；
- 不能依赖后台虚拟显示隐藏窗口；
- 镜像来源只保留 physical/system。

这是统一执行引擎的直接代价。

### 15.2 失去远程直接控制

镜像页不再直接发送鼠标键盘事件。用户需要让智能体执行操作。

这样可以避免：

- 两套输入协议；
- 浏览器输入与智能体输入竞争；
- 接管状态和恢复状态机；
- 远程控制与 agent-desktop 同时持有桌面。

### 15.3 平台能力不完全一致

`agent-desktop` 在三个平台支持范围不同。

要求：

- 不伪造跨平台一致性；
- 平台未支持的动作返回明确错误；
- 不使用 Multica 自研实现补齐；
- 不使用原始坐标注入假装支持。

### 15.4 高风险动作仍可能执行

`agent-desktop` 本身不负责业务权限。Bridge 必须保留工具审批和 runtime 级开关。

审批只决定是否转发，不解析或重写动作。

## 16. 评审确认项

已经由本方案确定：

1. 镜像页默认只读，人工接管必须显式批准并通过 agent-desktop 执行。
2. 同一 runtime 同时只运行一个桌面任务。
3. 固定 `agent-desktop v1.3.1`。
4. 不保留 feature flag、双写、运行时 fallback 或任意 `PATH` 版本。
5. Desktop 和 CLI 都携带三个平台的原生资产。

发布到生产前仍需产品确认：

1. 物理鼠标键盘 `--headed` 默认关闭，只在 runtime 显式开启。
2. 接受删除 `runtime_vscreen_intervention` 的线上数据；如需审计保留，只能先离线归档。
3. 接受 macOS、Windows、Linux 只承诺 `agent-desktop` 当前平台能力，不用 Multica 自研代码补齐。
4. 接受镜像页的人工接管只通过受批准的 `agent-desktop` session 执行，不恢复 Multica 自研远程直接输入。
