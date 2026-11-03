# 单链接 Skill 分发

把 `multica-onboarding.skill` 作为一个完整 ZIP archive 分发。一个 URL 必须下载到这个 archive，
而不是只返回单独的 `SKILL.md` 或需要浏览器登录后才能看到的 HTML 页面。

## Archive 合同

下载后的包必须包含：

```text
multica-onboarding/
├── SKILL.md
├── agents/openai.yaml
├── assets/multica-cli/
│   ├── SKILL.md
│   ├── LICENSE
│   └── agents/openai.yaml
└── references/
    ├── distribution.md
    ├── cli-install.md
    ├── global-skill.md
    ├── concepts.md
    ├── deployment-contract.md
    ├── cloud-machine.md
    ├── channels.md
    └── troubleshooting.md
```

这些文件共同构成 Skill。`SKILL.md` 中的 `references/...` 和 `assets/...` 都是 archive 内的相对路径，
不是 Multica 源码仓库路径。接收方 Agent 无需、也不应尝试 clone Multica 仓库来完成 onboarding。

## 给 Coordinating Agent 的动作

1. 从用户给出的 HTTPS URL 下载 archive 到临时目录，不执行其中的任意二进制。
2. 按发布方同时提供的 SHA-256 或签名验证 archive；缺少校验值时明确告知用户来源尚未验证。
3. 列出 archive，确认上述文件齐全且没有路径穿越条目。
4. 按当前 Agent harness 的 Skill 安装方式解压整个目录，不能只复制 `SKILL.md`。
5. 从 `SKILL.md` 开始，根据用户选择按需读取包内 reference。

若目标是导入 Multica Workspace，某些 URL 不能直接传给 `multica skill import --url`。先下载
archive，再运行：

```bash
multica skill import --file ./multica-onboarding.skill
```

发生同名冲突时先展示现有 Skill 的来源和版本，再由用户决定是否使用 `--on-conflict overwrite`；不要
默认覆盖。

## 分发边界

- Archive 只包含 onboarding 知识和无凭据的全局 Skill 模板，不包含 Multica CLI、GitLab Token、Bot
  Secret、设备代码或登录态。
- Skill URL 可以独立于 HTML 操作手册；HTML 应链接到 archive，但不能代替 archive。
- 正式静态地址必须能被 Agent 直接下载，不能依赖只对交互式浏览器开放的 SSO 页面。
- 发布方应使用不可变版本 URL 和 digest；`latest` 只能是指向不可变 release 的可审计指针。
