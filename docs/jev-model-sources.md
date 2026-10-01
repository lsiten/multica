# 工作区 Jev 三来源配置增量方案

状态：新增需求设计已明确；本文件不是已实现声明。日期：2026-10-01。

本需求覆盖此前附件中“不引入本地模型/Python 推理依赖”的限制：桌面端提供模型目录和按需下载入口，用户选择 Mapika/decider-2b 后点击下载。桌面安装包不包含模型权重，安装、升级桌面端以及仅切换模型选项均不触发下载。模型仍在宿主机运行，由 daemon 承载，不恢复 VM/SmolVM 或支付能力。之前 code review 的问题仍需修复，不能用新模型选项代替整改。

## 产品入口和作用域

入口：工作区 → 设置 → Jev 决策模型。三种来源为 local、agent_context、remote，保存一个明确生效的来源。默认使用 agent_context，避免未经选择即下载模型或把上下文发送到新远端。

工作区保存来源策略和参数；实际任务按所选 Agent 的 runtime 找到所属 daemon。Web 用户点击下载时，目标必须是这个 daemon 所在机器，不是浏览器电脑或 API 服务器。页面显示执行机器、daemon 在线状态及所需管理权限。离线时不显示安装成功或服务就绪。

同一工作区可以包含多个 runtime；本地模型的安装、磁盘缓存和就绪状态逐 daemon 显示。不得用 A 机器已安装的状态授权 B 机器直接运行。

## 三种来源

| 来源 | 配置和执行 | 结果语义 |
|---|---|---|
| local | 选择 Mapika/decider-2b；选择经验证的固定 revision/推理引擎；确认下载后，由目标 daemon 下载、校验、启动 | 模型实际决策分布；记录具体模型、revision、设备、温度/校准元数据，不保证所有场景都可靠 |
| agent_context | 当前正在运行的 Agent 使用已有任务上下文和显式候选/标准判断，不另起独立模型进程 | 主 Agent 上下文语义判断；记录 agent/session/task 与 source，不能冒充独立验证或 logits 概率 |
| remote | 配置 Jev/SystemOne 端点、认证密钥引用、模型标识、协议版本、超时和并发；由目标 daemon 连接 | 根据远端能力探测记录返回分布/语义来源；没有能力声明和结构校验就不进入 ready |

协议来源与模型来源分开：Mapika/官方 Jev 使用 Jev/SystemOne 适配器；普通 OpenAI-compatible endpoint 若支持，另明确选择 semantic adapter。不能把任何远端 URL 自动拼上 /chat/completions 后当作 Jev 服务。

## 本地下载和生命周期

桌面交互流程：选择「本地模型」来源 → 选择模型 → 点击「下载模型」并确认大小与目标机器 → 显示下载进度 → 校验完成 → 显示「已下载」 → 选择启用。下载确认只针对这次模型资源安装，不增加日常任务的逐步授权。

已下载的完整版本可直接选择使用，不重复下载。仅选中未下载模型时显示「未下载」和下载按钮，不将它标为可用。模型文件独立保存在目标 daemon 的受管缓存中，不随桌面安装包分发，也不因普通桌面升级而重新下载。

内置目录至少列出 Mapika/decider-2b。默认只下载用户确认的固定模型 revision，目录名称 main 不能作为可重现版本。下载页展示版本、许可、权重大小、下载目标机器、预计空间和实际设备兼容性；官方资料对不同版本/设备的指标不完全相同，不能将 GPU 基准套用到普通 Mac/CPU。

状态：not_installed → downloading → verifying → installed → starting → ready；另包含 failed、stopping、stopped。安装成功与加载就绪分别呈现。支持已下载模型选择、进度、取消、失败重试、已用磁盘空间、服务停止和清理。下载中断不能留下可被选为完整模型的半成品。

- 安装资源由 daemon 统筹，放独立受管环境，锁定推理包版本，不改用户全局 Python。
- 校验固定 revision、模型文件和兼容推理包；默认不执行模型仓库任意 remote code。
- 服务监听 loopback，模型服务前加 daemon 私有认证边界；公开网络接入必须使用 remote 模式的认证契约。
- 同一 daemon 内相同模型版本/引擎/设备共享单个受管服务；不为每个 Agent 或每次决策启动一个模型。
- 使用活动请求计数、服务代次和空闲回收；单个任务取消只释放其请求，不能停止其他 Agent 正在使用的服务。
- daemon 重启需重新核验进程归属、模型版本和健康状态。下载缓存与推理实例状态分开；模型缓存不能放在 ClaimEnvRoot 会重置的任务目录。
- CUDA/MPS/CPU 由已固定推理版本的能力检测决定；降级设备、OOM、依赖缺失要显示真实错误，不静默标 ready。

## 当前上下文决策

当前 Agent 已持有完整任务上下文，因此提供明确的上下文决策指令和结构化结果记录工具。该模式不要求从 Codex/Claude 私有认证中提取密钥，也不假设向同名模型 endpoint 发新请求就能获得当前会话记忆。

判断结果按候选/等级/条件严格校验，附证据引用；验证工具校验结构和引用范围，不将主 Agent 的自评包装为独立审查。无法确定时返回 uncertain，交由任务既有授权策略处置。Jev 模型输出不能提升文件、通讯、审批或其他权限。

若后续需要独立的同模型二次验证，应作为另一种明确的执行方式，要求可用接口及显式上下文快照，不能悄悄改变 agent_context 的含义。

## 统一请求、结果与审计

对外保持统一类型：Choice（有穷候选）、Score（有描述的等级）、Noul（二元问题）。不同来源适配为一致的字段名，保留源协议真实语义。

结果至少包含 source、provider、model、revision、task_id、decision_id、verdict/value、evidence_refs、latency、usage、usage_known。概率和校准字段只在来源确实提供对应信息时出现；语义判断不制造 logits、校准置信度或伪造 Token 数。

超时、输入过大、下载未完成、daemon 离线、模型未就绪、协议不匹配、鉴权失败、未知用量分别有可识别错误。切换来源只对新任务生效；进行中的任务使用开始时的配置 revision，防止半途更换模型或远端。

本地推理、远端 Jev 请求都接入任务统一用量收集器；当前上下文模式复用主 Agent 已记录用量，不重复计费。无法提供精确 Token 用量的来源必须标 unknown，不能写成零并声称满足硬 Token 限额。必须先修复 RT-02/RT-03 的实时快照与受管模型用量缺口。

失败默认明确返回，不自动切换来源。允许项目显式配置 fallback 顺序及触发条件；不能在本地错误时未经配置把内容上传远端。新旧来源的 schema/threshold 兼容性需验证。

## 代码集成边界

沿现有 server/internal/llm2jev 与 daemon managed MCP 扩展：

1. 工作区级配置 schema/DB/API：版本化策略、credential reference、能力与错误码。API 服务器负责权限、保存和路由，不启动本地推理。
2. daemon 模型管理器：下载作业、共享模型服务、健康/资源/日志、cancel/stop/cleanup。
3. 三来源 provider：local systemone、agent_context、remote systemone；保留现有 semantic adapter 的显式兼容选择。
4. task claim：携带项目配置快照与受限凭据引用；真实秘密不进入模型提示或普通列表响应。
5. Web/Desktop 共享项目配置页：来源选择、目标 runtime、下载确认、已安装选择、远端配置及连通检查。
6. 补足 Choice/Score/Noul 协议核心与预算、撤权、并发和重派回归，再做真实验收。

## 验收条件

- 三种来源都能从工作区设置页面选择并保存，新任务在目标 daemon 上确实生效。
- 桌面安装包无模型权重；安装、升级、选择未下载模型不产生模型下载；只有点击下载并确认后才开始。
- 未确认不下载；下载取消/重试/半成品隔离正确；相同 daemon 多 Agent 只加载一份同配置模型。
- 本地实际权重推理通过 Choice/Score/Noul；在真实 CPU/MPS/CUDA 中仅对实际测试设备报告通过。
- agent_context 不另开推理服务，不虚构会话继承、独立验证或概率。
- remote 有协议和认证探测；认证错误、超时和重定向不能泄露密钥。
- 限额和用量覆盖所有额外推理；未知用量状态可见；来源切换与任务取消不影响其他请求。
- 项目/工作区/daemon 的授权与日志隔离、UI 状态、下载任务生命周期都有测试证据。

## 核实依据与当前限制

官方模型说明列出 Choice/Score/Noul 与 SystemOne 接口，推理库提供 CUDA/MPS/CPU 路径。将其当成通用聊天补全模型配置不符合原生协议。

- 模型：https://huggingface.co/Mapika/decider-2b
- 推理库：https://github.com/Mapika/decider
- 服务说明：https://github.com/Mapika/decider/blob/main/docs/SERVING.md

当前实现已提供工作区级配置 API、设置页入口、daemon 受保护的目录状态/显式下载接口，以及 local/remote SystemOne MCP 适配；仍未在本机下载模型或完成真实 GPU/CPU 全平台验收。本文不表示某台机器上的模型已经下载或服务已经启动。
