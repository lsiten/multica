## TL;DR (For humans)

将 `multica-llm2jev` 和 `multica-identity-actions` 从“任务启动时才创建的临时 HTTP MCP”升级为 daemon 启动时创建的内置 broker。broker 常驻并可被桌面端健康检查；每个任务只注册一份短期上下文，调用时由 broker 校验 task/workspace/agent 权限并解析该任务的 Jev provider 或 identity capability。这样桌面端启动即可显示“服务在线”，同时不泄露任务凭据，也不绕过现有 scope 和授权。

## Scope

- 统一 built-in MCP broker 生命周期：daemon `Run` 启动、重启/关闭清理、health/readiness 返回 broker 与 task-context 两层状态。
- `multica-llm2jev`：保留本地 loopback MCP 协议，broker 根据 task context 使用 workspace Jev 快照、Agent 上下文或本地模型 lease；明确返回 provider unavailable、credential missing、model unavailable 等诊断。
- `multica-identity-actions`：broker 常驻，但只有带有效 task context 且 capability 明确允许时才暴露/执行身份工具；所有调用继续携带 task authorization 和 task id。
- Desktop MCP readiness UI：区分 `broker_ready`、`task_context_ready`、`provider_unavailable`、`capability_required`，不再把 `task_not_started` 显示为“未下载”。
- 更新内置 MCP 文案、daemon types、API/IPC 校验和测试。

不在本次范围内：把 workspace MCP library、第三方远端 MCP、任意 stdio MCP 改成 daemon 常驻；它们仍由 workspace/agent/task 生命周期管理。

## Verification strategy

- Go daemon unit tests：broker startup/shutdown、路由隔离、task context 注册/过期、Jev source matrix、identity capability matrix、并发调用和 token 失效。
- Go daemon health tests：桌面启动无任务时两个 broker 都报告 `broker_ready`，不报告错误的 `task_not_started`；任务上下文缺失时报告可操作原因。
- Frontend tests：readiness schema/status mapping、settings card copy、未知状态 fallback。
- Existing targeted suites：`go test ./internal/daemon -run 'MCP|Jev' -count=1`、views settings tests、typecheck/lint。
- Full verification after changes：`make test`、`pnpm typecheck`、`pnpm lint`，并在本机已安装桌面 daemon 上用 `/health` 和一次允许/拒绝 identity、一次可用/不可用 Jev 任务做 smoke。

## Execution strategy

先抽取可复用的 broker 生命周期和 task-context registry，再迁移两个现有 task MCP server。每一步先保持现有 task URL 兼容，完成新 broker 的协议测试后切换 daemon task overlay。最后更新 health/IPC/UI 和文案，确保旧 desktop client 仍能读取新增状态字段并回退到已有状态。

## Todos

- [ ] 1. 定义 built-in broker 与 task-context contract
  Recommended task executor category: worker-medium
  Scoped files: `server/internal/daemon/mcp_*`, `server/internal/daemon/types.go`
  Dependencies: none
  Acceptance: broker/context state enums、短期 token、workspace/task/agent/capability 字段和错误码明确；不携带 API key 或 SMTP secret 到 health/UI。
  QA: contract/unit tests cover unknown state and expired context.

- [ ] 2. Add daemon-start broker lifecycle
  Recommended task executor category: worker-high
  Scoped files: `server/internal/daemon/daemon.go`, `server/internal/daemon/health.go`, new broker files
  Dependencies: 1
  Acceptance: daemon startup binds loopback broker endpoints; shutdown/restart closes them; `/health` reports broker readiness with zero active tasks.
  QA: startup/close/restart tests and port collision diagnostics.

- [ ] 3. Migrate llm2jev to broker-backed routing
  Recommended task executor category: worker-high
  Scoped files: `server/internal/daemon/llm2jev_mcp.go`, `server/internal/daemon/jev_configured_mcp.go`, task claim/context path
  Dependencies: 1, 2
  Acceptance: desktop-start broker is reachable; task registration binds Jev snapshot/provider; calls use the bound provider and return structured provider/credential/model errors; no cross-task context access.
  QA: local/remote/agent_context matrix, concurrent task isolation, provider unavailable diagnostics.

- [ ] 4. Migrate identity-actions to broker-backed authorization
  Recommended task executor category: worker-high
  Scoped files: `server/internal/daemon/identity_actions_mcp.go`, task authorization/context path
  Dependencies: 1, 2
  Acceptance: broker starts at daemon startup; identity tools are hidden or denied without task capability; allowed calls use task-scoped task id and receipt path; no global credential exposure.
  QA: allowed/denied/expired task context and concurrent task tests.

- [ ] 5. Wire readiness health, desktop IPC, and settings UI
  Recommended task executor category: worker-medium
  Scoped files: `server/internal/daemon/health.go`, `apps/desktop/src/main/daemon-manager.ts`, `apps/desktop/src/shared/daemon-types.ts`, `apps/desktop/src/preload/*`, `packages/views/settings/components/mcp-readiness-card.tsx`, locale files
  Dependencies: 2, 3, 4
  Acceptance: status distinguishes broker online from task/provider state; no false “未下载”; old clients safely fall back; UI exposes actionable Jev/provider/capability guidance.
  QA: schema/status tests and locale parity.

- [ ] 6. Sync docs and operational guidance
  Recommended task executor category: worker-low
  Scoped files: `server/internal/service/builtin_skills/multica-platform/**`, deployment/release notes as needed
  Dependencies: 3, 4, 5
  Acceptance: docs state broker startup, task authorization, Jev sources, failure recovery, and do not claim identity actions are globally authorized.
  QA: docs link/reference checks.

- [ ] F1. Run final backend, frontend, daemon smoke, and diff hygiene checks
  Recommended task executor category: worker-medium
  Scoped files: repository-wide verification only
  Dependencies: 1-6
  Acceptance: all targeted and required full checks pass; installed daemon reports expected broker state; no tracked unrelated changes.

## Final verification wave

- [ ] F1. `go test ./internal/daemon -count=1`
- [ ] F2. `make test`
- [ ] F3. `pnpm typecheck`
- [ ] F4. `pnpm lint`
- [ ] F5. Desktop local `/health` and settings MCP readiness smoke with no task, allowed task, denied task, and unavailable Jev provider

## Commit strategy

Use atomic commits by lifecycle, broker routing, UI/contract, and documentation. Do not create a release tag until the packaged desktop daemon version is rebuilt and the smoke confirms the installed binary contains the broker changes.

## Success criteria

- Opening the desktop app starts both built-in broker services and health reports them online without requiring a task.
- A task receives only its own Jev and identity context; cross-task/workspace calls are rejected.
- Jev configuration is actually used at task claim/start, with actionable provider errors instead of a generic unavailable result.
- Identity tools are available only under explicit capability authorization.
- Settings no longer presents task-scoped absence as “not downloaded”.
- Backend/frontend tests and packaged desktop smoke pass before release.
