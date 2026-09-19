# PRD：运行时镜像交互（远程操作与智能体代操作）

## 1. 背景

运行时镜像（见 [runtime-mirroring.md](./runtime-mirroring.md)）当前只支持只读观看：屏幕像素经 WebRTC 在浏览器与 daemon 间点对点传输，服务端不经手画面。本需求在不破坏该隐私边界的前提下，为镜像增加“操作”能力，包含两种主体：

1. **人直接操作**：授权用户在镜像页手动进入交互模式，用鼠标/键盘操作正在观看的屏幕。
2. **指挥智能体操作**：用户在镜像页底部向绑定该 runtime 的智能体下达自然语言指令，由智能体观察屏幕并代为操作。

两者在 daemon 侧汇聚到同一个动作仲裁器，按“先到先得（FCFS）”争夺同一显示资源；不同显示资源（虚拟屏 vs 物理屏）之间天然不互斥。

## 2. 目标

1. 镜像页支持手动开启/关闭“交互模式”，默认始终为仅查看。
2. 交互模式下，人的指针、滚轮、具名按键与屏幕外文本输入经既有 WebRTC DataChannel 回传 daemon 并注入，服务端不经手输入 payload。
3. 镜像页底部提供统一操作栏：屏幕外文本框、绑定智能体选择器、发送路由（注入屏幕 / 发给智能体）与常用组合键。
4. 用户向选定智能体下达的指令复用现有聊天会话；智能体默认在虚拟屏操作，仅当用户在镜像页显式选中某物理屏并下达指令时，才在该指定物理屏上操作。
5. 人的输入与智能体的动作在 daemon 内按“显示资源 + 原子手势”做 FCFS 仲裁；不冻结任一方，抢不到资源的一方立即收到 busy 并各自重试。
6. physical/system 屏的人工操作需要显式主机授权与系统辅助功能权限；主机保留一键夺回（emergency stop）。

## 3. 非目标

- 不让服务端转发、记录或存储任何输入事件、文本内容、坐标流或像素。
- 不新建 workspace/runtime 级“可控制成员名单”或新角色；控制资格完全复用现有 runtime 可用性权限。
- 不在屏幕上拦截物理键盘以输入任意文本；文本统一由屏幕外输入框以语义 `type` 注入（规避输入法/键盘布局问题）。
- 不实现远程桌面式的多人协作光标、排队锁、会话级独占；锁仅在原子手势期间持有。
- 不改变项目、智能体、运行时的现有数据归属；不为每条屏幕指令新建任务。
- Linux 首版不支持交互操作，入口须明确说明“不支持”，与镜像首版一致。

## 4. 关键概念与主体

| 概念 | 说明 |
| --- | --- |
| 查看者 viewer | 建立只读镜像会话的用户，可多人共享同一捕屏源。 |
| 控制者 controller | 手动进入交互模式、获得 control grant 的人。同一资源同一时刻只接受一个原子手势。 |
| 智能体主体 agent principal | 绑定该 runtime 的智能体，借由聊天会话产生观察与动作；其动作携带任务/租约身份。 |
| 本机用户 local principal | 物理坐在主机前、持有真实键鼠的人，始终最高优先且不受远程锁限制。 |
| 资源 resource | 以 `ResourceKey` 精确标识的一块显示（virtual/physical/system 中的一个 source）。 |
| 原子手势 gesture | 一次 click、一次 drag（down→up）、一段 type、一个按键 chord；锁的最小持有单位。 |

## 5. 权限与授权

复用现有 runtime 权限（`canUseRuntimeForAgent`），不新增名单：

| runtime 可见性 | 可观看 | 可申请交互控制（资格） |
| --- | --- | --- |
| `private`（默认） | 仅 owner | 仅 owner |
| `public` | workspace 成员 | workspace 成员 |

“资格”不等于“此刻可操作”。人的交互操作需同时通过三道相互独立的闸：

1. **服务端资格**：control grant 端点复用 `requireRuntimeReadAccess` 同一 gate（`private` 仅 owner，`public` 成员即可）。`public` 的语义因此扩展为：在主机交互开关打开时还包含“成员可远程操作”，需在可见性切换与交互开关处给出明确后果文案。
2. **主机侧同意（人专属，默认关）**：daemon/桌面端维护“允许远程交互”运行态开关，默认仅查看。关闭时即使服务端授权，交互申请也被 daemon 拒绝。该闸只约束“人”，不约束智能体。
3. **系统权限**：操作 physical/system 需要 macOS Accessibility / Windows 等效的输入注入授权（与 Screen Recording 是两个独立 TCC 授权）；权限状态沿用现有 `vscreen state.permissions` 观测。virtual 使用既有 per-PID 后台注入，不需要全局输入授权。

智能体操作授权：

- 默认允许智能体操作，且默认目标为**虚拟屏**，无需进入交互模式。
- 仅当用户在镜像页显式选中某个 physical/system source 并对智能体下达指令时，智能体才操作该指定屏；“显式选择屏幕 + 下达指令”是触发物理屏操作的必要条件，不提供静默的物理屏自主操作。
- 智能体对物理屏的动作与人类输入共用同一全局注入器和仲裁器，并同样受 emergency stop 与本机用户优先权约束。

## 6. 交互模式状态机（人）

```
                 手动“进入交互”            建立 input 通道 + 获得 control grant
仅查看(默认) ───────────────▶ 申请中 ────────────────────────────▶ 交互中
   ▲                              │ 失败(离线/主机开关关/无权限/被占)        │
   │                              └──────────────▶ 仅查看(展示原因)        │ 退出/切走页面/
   │                                                                      │ grant 过期/断链/
   └──────────────────────────────────────────────────────────────────────┘ emergency stop
```

- 仅查看时不建立 `mirror-input` 通道、不申请 grant，画面点击不落到底机。
- 进入交互是显式手动动作；退出页面、切回查看、grant 过期或 PeerConnection 断开即释放。
- 被仲裁拒绝（资源正被另一手势占用）时不退出交互模式，仅给出瞬时“正忙/被挡”反馈，因为原子手势为毫秒级。

## 7. 通道与协议

在既有 P2P 拓扑上新增反向数据通道，服务端仍只做信令：

```
浏览器镜像页
  ├── mirror            帧, daemon→浏览器（现状）
  ├── mirror-control    视频元数据/错误（现状，已占用，不混用）
  └── mirror-input      输入, 浏览器→daemon（新增，P2P，服务端永不见 payload）
                 ▼
        daemon InputArbiter（按 resource 仲裁）
           ├── human principal  {user, viewer, control_grant}
           ├── agent principal  {task, lease_epoch}
           └── local principal  （本机用户，最高优先）
                 ▼
        native 注入器
           ├── virtual         → 复用 appcontrol per-PID 后台注入
           └── physical/system → 新增全局注入（macOS CGEventPost / Windows SendInput）
```

输入消息（独立于帧的二进制分包协议，建议紧凑二进制或受限 JSON）：

- `pointer:move | down | up | wheel`：坐标为帧像素 `VscreenPoint`，并携带当前 `geometry revision`；native 负责到目标显示的坐标变换。
- `key:down | up`：仅限既有已验证的具名键集合（A–Z、0–9、Enter/Return、Tab、Space、Escape、Backspace、Delete、方向键）与修饰键（shift/control/alt/meta）。
- `type`：来自屏幕外输入框的 Unicode 文本，走语义文本输入路径，不改全局剪贴板。
- 每条消息携带 `epoch(native/display/geometry)`、单调序号与 `gesture_id`（同一拖拽/一串按键共享）。
- daemon 回 `input:ack | input:nack(reason=busy|stale|denied|unsupported)`。

control grant：

- 新增 `MirrorControlGrant`，结构对齐 `MirrorViewerGrant`：绑定 `workspace/runtime/user/viewer/source/native_epoch/expires_at`，服务端签发、短 TTL、可续期、daemon 校验归属与 epoch，断开即吊销。
- 它是显式的 input capability，与“只读、永不可作为输入凭证”的 viewer grant 严格区分，不互相复用。
- 新增 daemon capability（如 `screen-control-v1`）。旧 daemon 不声明时，交互入口置灰并提示升级。

## 8. FCFS 仲裁

- 仲裁器位于 daemon（所有 viewer PeerConnection 与智能体动作的唯一汇聚点），不依赖服务端多副本或分布式锁。
- 锁键为 `ResourceKey`：人在物理屏操作与智能体在虚拟屏跑任务分属不同资源，完全不互斥，因此默认情况下不会冻结智能体。
- 同一资源上，锁在**一个原子手势**期间持有；设最大持有时长，靠 `gesture_id` 心跳续期，主体断链强制释放。
- 智能体现有任务动作执行器在进入 `appcontrol`/全局注入前，先以 `{agent, task_id, lease_epoch}` 身份进入同一仲裁器；抢不到则按其既有 snapshot / needs_intervention 语义返回 busy，重新观测后重试。
- **不做排队**：对已变化的界面排队注入过期输入更危险；失败方立即 busy + 自行重试。
- 本机用户输入始终优先且不被远程锁阻塞。
- owner 不做抢占式插队（保持 FCFS 纯粹）；但 owner/本机用户保留 emergency stop，立即清空全部远程输入锁（人 + 智能体）。

## 9. 底部操作栏与智能体代操作

```
┌───────────────────────────────────────────────────────────────┐
│                     实时屏幕区域                                │
│   交互模式下在此采集指针/滚轮；画面标注当前控制者（人/某智能体）    │
└───────────────────────────────────────────────────────────────┘
[交互模式开关]  [智能体 ▾]  [ 屏幕外输入框................ ] [发送 ▾]
                                                 └ 路由：注入屏幕 / 发给智能体
```

- **屏幕外输入框**：所有 Unicode 文本在此输入后以 `type` 注入；支持粘贴长文本、常用组合键（⌘W / Alt+F4 / Esc / Enter）与 `/` 快捷指令，均走同一受控输入通道。
- **智能体选择器**：列出 `runtime_id` 等于当前 runtime 的已绑定智能体（system 与未归档 user agent）；默认不选中，唯一绑定时可便捷预填但不自动发送；无绑定时置灰并提示。
- **一个输入框、两种发送路由**：
  - 未选智能体（或显式切到屏幕）且处于交互模式：作为 `type` 注入当前画面焦点。
  - 选中智能体：复用现有聊天会话发送一条自然语言指令，不新建任务、不新建会话体系。
- **目标屏幕由画面上的 source 选择决定**：智能体默认操作虚拟屏；当用户选中某 physical/system source 后向智能体发送指令，该指令显式绑定该 source，智能体即在该屏操作。消息需携带所选 source 的身份（kind/source_id/epoch/generation），daemon 侧按现有 source 契约校验，缺失或过期则拒绝，不做静默回退。
- **会话回执区**：在屏幕与操作栏之间提供可折叠会话条，复用现有 DM/会话视图显示智能体的执行、求助（intervention）与完成，天然保留审计上下文。
- 指挥智能体与亲手操作不互斥：二者同进仲裁器；智能体操作期间用户可随时插手，画面需明显标注“智能体正在操作”。

## 10. 通知、可见性与审计

- daemon 将“当前谁在控制（某 human viewer / 某 agent task）”作为元数据，沿现有 viewer state 上报链路 fan-out，驱动所有观看者画面上的控制者标识与 busy 反馈。
- 首位控制者开始与最后一位离开时，向 runtime owner 发送“正在被控制 / 控制结束”系统通知，对齐现有“正在被观看”通知；内容只含身份、runtime、时间与目标 source，**不含按键、文本或坐标**。
- physical/system 被控时主机需有持续、明显的本地指示（真实光标会移动）。
- 审计仅记录：谁、何时、对哪个 runtime/source、人还是智能体、结果；不记录输入内容。

## 11. 隐私与安全红线

- 输入 payload 与帧一样仅在浏览器↔daemon P2P 传输；服务端日志、数据库、对象存储、审计与分析中不得出现按键、文本、坐标流或像素。
- 每条输入校验 control grant（人）或任务租约（智能体）+ viewer 归属 + epoch/source 绑定 + geometry revision，拒绝跨用户、跨 runtime、跨 source 与陈旧画面重放。
- 智能体对物理屏的操作必须由“选中物理屏 + 下发指令”显式触发，不存在默认/隐式的物理屏自主操作。
- emergency stop 必须能无条件即时终止全部远程输入并释放手势锁。
- 屏幕外文本可能含密码等敏感内容：相关日志默认脱敏；文本只用于本次注入，不落盘。

## 12. 技术改动面

- `server/pkg/protocol`：`MirrorControlGrant`、`mirror-input` 消息（pointer/key/type/gesture + epoch/geometry + ack/nack）、`screen-control-v1` capability、控制状态事件；智能体指令携带的显式 source 契约。
- `server/internal/mirror`：协商 `mirror-input` 通道（避开已占用的 `mirror-control`）、control grant 生命周期（签发/续期/吊销）。
- `server/internal/vscreen/native/input`（新）：physical/system 全局注入（macOS `CGEventPost`、Windows `SendInput`），含与 appcontrol 等价的手势释放与崩溃清理围栏；virtual 继续复用 `appcontrol`。
- `server/internal/daemon`：按 resource 的手势级 FCFS 仲裁器，统一 human/agent/local 三类主体；人类交互主机开关、TCC 观测、控制状态 fan-out、“正在被控制”通知、emergency stop；智能体动作执行器接入同一仲裁器。
- `server/internal/handler`：control grant 端点复用 runtime 可用性 gate；按 runtime 列绑定智能体（复用现有查询）；聊天发送复用现有会话接口。
- `packages/core`：control grant API、agent-by-runtime 查询、输入消息编解码与类型；聊天复用现有 mutation。
- `packages/views/runtimes/components/mirror`：交互模式开关与状态机、底部操作栏（屏幕外输入框/智能体选择/发送路由/组合键）、可折叠会话条、控制者标识与 busy 反馈、物理屏选中后对智能体指令的 source 绑定。
- 数据库：不新增权限表/名单；主机交互开关为 daemon/桌面端运行态设置；如需要持久化“允许远程交互”偏好，挂在现有 runtime/daemon 设置结构内，不引入新权限模型。

## 13. 落地切片

1. 协议与 grant：`MirrorControlGrant`、`mirror-input` 通道、capability 与 TCC 观测；control grant 端点复用现有 gate。
2. 人工操作 virtual：daemon 仲裁器 + 复用 appcontrol，打通手势锁、ack/nack、epoch/geometry 校验与交互模式前端。
3. 物理屏人工操作：新增全局注入器 + Accessibility 授权 + 主机交互开关 + 本地指示/emergency stop，接通 physical/system。
4. 智能体并入：任务动作执行器接入同一仲裁器；底部操作栏通过现有聊天会话指挥智能体，默认虚拟屏，选中物理屏时绑定显式 source。
5. 状态/通知/审计/文案与可访问性收尾，macOS、Windows 各至少一台真机验收。

## 14. 验收标准

1. 镜像默认仅查看；不手动进入交互模式时，任何点击/键入都不影响底机。
2. private 机器仅 owner、public 机器 workspace 成员可申请交互；主机交互开关关闭时申请被明确拒绝。
3. 进入交互模式后可在 virtual 屏完成点击、拖拽、滚轮、具名按键与屏幕外文本输入；服务端日志/存储中无输入内容。
4. physical/system 屏在授予辅助功能权限后可操作，且主机有持续被控指示；emergency stop 能立即终止所有远程输入。
5. 两个用户、或一个用户与一个智能体争抢同一资源时，按原子手势 FCFS，败者收到 busy 且不产生撕裂操作；人与智能体分处虚拟屏与物理屏时互不阻塞。
6. 底部可选择绑定该 runtime 的智能体并复用现有聊天会话下达指令；默认操作虚拟屏，选中物理屏后指令仅作用于该屏，source 过期时被拒绝而非回退。
7. 不为每条屏幕指令新建任务或新会话；指令与回执出现在既有会话中。
8. 控制者开始/结束向 owner 发出系统通知，且不含输入内容；所有观看者能看到当前控制者。
9. 旧 daemon、离线 runtime、缺失系统权限、Linux runtime 均显示对应不可用状态，无空白页或无限重试。
10. macOS、Windows 各至少一台真机完成人工操作与智能体代操作验收；Linux 明确提示暂不支持。