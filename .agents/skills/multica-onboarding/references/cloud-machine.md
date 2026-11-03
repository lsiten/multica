# Cloud Machine onboarding

Cloud Machine 是用户拥有的个人云端电脑，不属于某一个 Workspace。一台机器可以服务多个
Workspace，但每个 Workspace 的 Runtime 和文件上下文仍然隔离。

可用性、规格和授权方式取决于具体部署；若入口、规格或授权方式未出现在当前页面，不要用文档描述推断
它已开放，以当前页面为准。

## 创建和准备

由当前 Workspace 的 owner/admin 从 Multica Web 打开 Cloud Machine 创建向导；system admin 身份本身
不授予创建权限。按五步完成：

1. 填写机器名称。
2. 选择 `Small`（2 CPU/4 GiB，最多 2 个并发任务）或 `Medium`（4 CPU/8 GiB，最多 4 个并发任务），
   以当前页面提供的规格为准。
3. 选择 LLM 认证方式。
4. 选择要初始化的代码仓库；也可以跳过，创建后再添加。
5. 确认配置并创建。

创建后分别等待 Machine 运行中、当前 Workspace Runtime online 和目标 LLM Auth ready；三者是独立
状态，不能互相替代。从机器详情页进入 Console；普通用户是 `node`，工作目录、登录状态和用户配置在
正常重启或镜像更新后持久保留。关闭 Console 只会断开浏览器连接，不会关闭机器或任务。

## Codex 与 Pi 认证

OpenAI Codex OAuth 供 Codex Runtime 和 Pi 的 Codex 模型使用。用户需要先在 ChatGPT 的账户安全
设置中允许 Codex 设备代码授权，再从 Cloud Machine 详情页启动登录：

1. 打开页面给出的设备登录链接并完成人工授权。
2. 回到 Multica 页面等待自动检查，或点击“已完成，检查状态”。
3. 链接到期后换码，不复用失效代码。

关闭页面不等于取消登录任务。不要复制设备代码、refresh token 或个人订阅凭据到 Issue/聊天。
某些部署可能提供额外的网关或 provider 授权；只有当当前页面明确列出时才可配置，不要把一个 provider
的 ready 状态解释为所有模型都可用。

## 添加和管理仓库

创建向导一次可添加多个 GitLab 仓库（以当前页面给出的上限为准）；创建后也可在机器详情页添加、修改
ref、调整个人凭据仓库的只读/读写模式或异步移除。用户只填写仓库 URL、凭据来源、branch/tag/commit
和访问模式，不配置本地目录或 sparse/partial clone 策略。

- 已接入 AI Code Review 的仓库可无需 Token 只读添加。
- 需要未接入仓库或写操作时，使用用户自己的 GitLab connection/Token，并显式选择 `read_write`。
- 仓库属于 Cloud Machine，不属于当前 Workspace；同一机器上的 Codex、Pi 和 Multica task 看到相同
  仓库状态。
- Base Checkout 是只读同步视图。branch 默认周期同步；tag、commit 或 dirty Base 不会自动漂移。
- 删除仓库会异步清理；dirty、active、pinned 或异常内容会保留并要求恢复，不能假装已经删除。

## Agent 通过 Repo Manager 写代码

Repo Manager 把仓库以机器级受控状态接入 Cloud Machine：

- Base Checkout 是只读同步视图；Agent 修改应发生在隔离 Worktree。
- 新工作应使用 `repo-manager worktree create-branch` 从远端最新 branch 或明确 SHA 创建新的远端短分支
  和 Server-authorized Worktree；不要从可能过时的 Base Checkout 猜起点。
- 可写仓库只能通过 `repo-manager worktree push <worktree-id>` 把当前 Worktree HEAD 推到授权 branch。
- push 后使用 `repo-manager merge-request upsert` 创建或更新同一 open GitLab MR；该命令不 merge、tag、
  发布或部署。
- 仓库凭据、临时 Git session 和机器 PAT 不应进入 Agent 输出或持久 Git 配置。
- 仓库 ref、access mode、revision 和 Worktree 状态以当前机器生成的 repository context 为准，不能凭
  记忆推断。

Repo Manager 的“仓库可读”不等于“任意分支可写”。创建 branch、push 和 MR 前分别验证当前 Binding
为 read-write、source/target ref 存在且权限允许；MR 建立后仍需测试、独立审阅和仓库门禁。

## 完成标准

- Machine ready；
- 目标 Workspace 的 Runtime online；
- 所需 provider 的 LLM Auth ready；
- Console 可用且不暴露平台凭据；
- 如需代码，仓库 Binding ready、revision 明确、访问模式符合任务；
- 一个真实业务任务由用户明确触发后可以完成并回到 Multica 审阅。
