# Multica 核心概念

## 一句话模型

Multica 把人、智能体、工作流入口和可执行环境连接起来。用户在 Workspace 中通过 Chat 或 Issue
表达工作，由 Agent 使用选定 Runtime 执行；结果回到原工作流接受人类审阅。

## 对象边界

- **Workspace**：成员、Agent、Skill、Issue 和渠道安装的租户边界。
- **Agent**：指令、模型工具和 Skill 的组合，不等于运行它的电脑。
- **Runtime**：某个 Workspace 中可领取 Agent 任务的执行投影；本地 daemon 和 Cloud Machine 都可提供
  Runtime。
- **Issue**：可分解、可追踪、可审阅的异步工作单元。创建、分配和评论都可能产生副作用。
- **Skill**：按场景加载的操作知识；它不能替代服务器权限、人工身份认证或发布门禁。
- **渠道入口**：把群聊等外部会话映射到 Workspace 和 Agent。渠道只改变入口，不改变 Workspace
  隔离边界。

## Cloud Agent 不只是 Cloud Model

远端模型只解决“在哪里推理”。完整的 Cloud Agent 还包含：

```text
人 / 渠道入口
        ↓
Multica Workspace + Agent + Issue + 审阅
        ↓
Cloud Runtime
        ↓
个人持久 Cloud Machine + 隔离 Sandbox
        ↓
Codex CLI / Pi 等 agent harness
        ↓
Repo Manager 提供受控仓库上下文
```

因此它同时提供持久状态、隔离执行、Agent harness、身份认证、仓库上下文、渠道入口和可追踪工作流。
Cloud Machine 的开放程度、规格和授权方式取决于具体部署；不要凭文档推断某个能力已开放，以当前
页面为准。

## 本地与 Cloud 的选择

| 需求 | 本地 Runtime | Cloud Machine |
|---|---|---|
| 继续使用个人电脑已有工具 | 更直接 | 需要在云端重新确认工具和认证 |
| 人离线后继续运行 | 依赖个人电脑在线 | 更适合 |
| 持久且与个人电脑隔离的环境 | 受本机影响 | 更适合 |
| 访问本机未接入的私有文件 | 更直接 | 必须通过受控仓库或上传路径 |
| 可复现的共享基线 | 取决于个人环境 | 镜像和受控配置更稳定 |

“Cloud 更好”不是默认结论。根据数据边界、运行时长、工具依赖和访问权限选择。
