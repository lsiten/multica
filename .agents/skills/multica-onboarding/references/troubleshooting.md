# Onboarding 故障诊断

先分类，再修改。每次只验证一个假设，并在摘要中脱敏。

| 现象 | 先检查 | 安全处理 |
|---|---|---|
| `multica` 不存在 | OS/Arch、GitHub Release API 可达性 | 按 `cli-install.md` 用命令行下载精确官方制品、验证 checksum、先在临时目录运行；不引导去官网下载页 |
| GitHub Release 下载失败 | 普通公网访问、DNS、代理 | 不切换内网路由；报告公网下载问题，不执行来源不明的镜像脚本 |
| 用户装了或打开了 Multica 桌面端，卡在登录 | 是否桌面端、桌面端是否支持本部署的登录方式 | 桌面端可能只提供本部署不支持的登录方式；改用网页版 `<APP_URL>`，用本部署支持的登录方式登录 |
| 内网/私有部署入口不可达 | `curl --noproxy '*'`、DNS、是否启用了外网 VPN/代理 | `--noproxy` 能通就是代理拦截：保留代理、用 `NO_PROXY` 加部署主机直连（不要整体关代理，否则 Codex 连不上 OpenAI）；OS 级 VPN 需关闭或排除部署路由 |
| 候选 CLI 版本兼容性未知 | 部署 config 端点的 `server_version`（未打版本号的构建会不返回这个字段，不算错误）、只读 API 探针 | 保留现有 CLI；区分“下载已校验”“核心 API 兼容”“Runtime 兼容”，不凭版本号猜测 |
| URL 不匹配 | `multica config show` 与覆盖环境变量名 | 先说明 setup 会重置 profile：只保留新的 server/app URL，原来的登录和默认工作区都会清掉，之后要重新登录、重新选工作区；再运行完整 `setup self-host` |
| Server 不可达 / setup 看起来成功但没生效 | VPN/DNS/TLS/代理、API health；`multica config show`、`multica auth status` | Server 连不上时 setup 只打印警告、不改配置，**退出码仍是 0**，不能看退出码判断成功。修复网络后重试，每次都用 `config show` 和 `auth status` 复核 |
| 浏览器登录无法回调 | CLI 与浏览器是否同机、SSH 拓扑 | 使用命令打印的 SSH tunnel hint；仅在可直连时用 `--callback-host` |
| 登录无效/过期 | `multica auth status` | 重新 `multica login`；不要索取或粘贴 token |
| 用户询问是否要创建 Token | 是否可以完成浏览器登录/回调 | 默认不用手工创建；CLI 登录会自动创建 90 天 PAT 并以 `0600` 保存。仅在回调和 SSH tunnel 都不可用时让用户亲自在终端输入 PAT |
| setup 拒绝重启 daemon 并报错 | 是否存在 active task | 新配置和登录此时已经保存，不要重跑 setup；等任务完成后，在设好代理和 `NO_PROXY` 的终端里执行 `multica daemon restart` |
| 本地 Runtime offline | `multica daemon status --output json`、`multica daemon logs -n 200` | 修复 daemon 后再看 `multica runtime list --output json`；电脑睡眠或 daemon 退出时 Runtime 会掉线，见下文 |
| 看不到 Workspace，或网页登录后落到“创建工作区”页 | `multica workspace list --output json`；这次用的是什么登录方式 | **先别建工作区。** 通过本部署身份提供方登录会自动加入团队工作区；用另一种登录方式（如邮箱验证码）是另一个账号。确认登录方式后仍为空，找管理员检查默认工作区配置。未接受的邀请是次要原因：邀请要先在网页上接受才会出现 |
| 登录后是一个空账号、看不到原来的工作区 | 这次和以前分别用的是哪种登录方式、哪个账号 | 混用桌面端/网页、不同登录方式可能产生第二个账号。统一用网页 + 同一登录方式重新登录，`multica login` 重新绑定 CLI；多出来的空账号不要继续用，需要清理时找管理员 |
| Cloud Machine ready 但模型不可用 | Runtime 与对应 LLM Auth 两个状态 | 修复目标 provider 的授权；不要用另一个 provider 的 ready 代替 |
| Repo 可读但不能 push | access mode、授权 branch、Worktree 类型 | 不直接注入凭据或绕过 Repo Manager；让 owner 修正 Binding/branch |
| 渠道私聊不执行任务 | 当前是否私聊 | 私聊只绑定；回到已连接群聊 |
| 群聊一直提示绑定 | 用户、Workspace membership、Bot 安装 | 私聊完成绑定后回群重试 |
| daemon 拉起的 Codex 连不上 OpenAI（超时、`stream disconnected`、TLS/连接错误） | daemon 是不是从没设代理的终端（或桌面端、GUI）启动的 | daemon 继承启动它那个终端的环境。先用 `multica daemon status --output json` 和网页确认没有任务在跑（`restart` 自己不检查），再在设了 `HTTPS_PROXY`/`HTTP_PROXY` 且 `NO_PROXY` 含部署主机的终端里执行 `multica daemon restart`；只检查变量是否设置，不打印值 |
| daemon 连不上 Multica / Runtime 反复掉线，而网页正常 | 启动 daemon 的终端里 `NO_PROXY` 是否含部署主机 | daemon 自己和 Server 之间的 HTTP/WebSocket 也会走 `HTTPS_PROXY`。把部署主机加进 `NO_PROXY` 后，确认没有任务在跑，再在该终端里 `multica daemon restart` |
| daemon 起来了但没有 Codex Runtime / 任务报找不到 codex | 终端里 `command -v codex`、`codex --version` | 没装就先装 Codex。装了但只在交互 shell 的 PATH 里（nvm、Homebrew 写在 `~/.zshrc`）：daemon 会尝试用登录 shell 解析，可能要等一段时间；更快的做法是在能找到 `codex` 的终端里 `multica daemon restart`，或设置 `MULTICA_CODEX_PATH="$(command -v codex)"` 后重启 daemon |
| 任务报未登录 / 401 / 需要重新认证 | `codex login status`（Codex 版本没有这个子命令时，让用户直接执行 `codex login` 确认） | 让用户本人执行 `codex login`，完成后重跑任务；不要替用户处理凭据 |
| Runtime 在线，但任务失败或一直卡住 | `multica daemon logs -n 200`（找对应任务的报错），`codex login status`，代理设置 | 按日志里的错误归到上面几类（代理、Codex 未登录、找不到 codex）；修好后在网页上重跑任务，不要反复重派 |
| 电脑睡眠、重启、注销或 daemon 退出后 Runtime 掉线、任务中断 | `multica daemon status --output json` | daemon 没有开机自启，也不是系统服务。唤醒或重新登录后确认 daemon 在跑；没跑就在设好代理和 `NO_PROXY` 的终端里 `multica daemon start`，再重跑中断的任务。需要人离开后继续干活的，用 Cloud Machine |
| Runtime 在线，但没有 Agent 可派活 | `multica agent list --output json` 里有没有绑在这台 Runtime 上的 Agent | 不要以为会自动创建（通过身份提供方登录的用户可能不走上游向导）。按用户意愿用 `multica agent create --name <名称> --runtime-id <runtime-id>` 建一个，或从 `<GUIDE_URL>` 货架装数字员工；建 Agent 是写操作，先征得同意 |
| 粘贴 Multica URL 后 Agent 不调用 CLI | 全局 `multica-cli` Skill 是否完整安装、harness 是否重载、description 是否识别本部署 URL | 按 `global-skill.md` 安装整个 bundled Skill，重启 harness，再做只读链接验证 |

## 最小诊断集合

```bash
curl -fsS -o /dev/null --connect-timeout 5 --max-time 15 \
  https://api.github.com/repos/multica-ai/multica/releases/latest
curl --noproxy '*' -fsS -o /dev/null --connect-timeout 5 --max-time 15 \
  "<SERVER_URL>/health"
multica --version
multica config show
multica auth status
multica daemon status --output json
multica workspace list --output json
multica runtime list --output json
command -v codex && codex --version
codex login status
[ -n "$HTTPS_PROXY$HTTP_PROXY$https_proxy$http_proxy" ] && echo "proxy: set" || echo "proxy: not set"
```

## daemon 日志与重启

以下命令以 `multica daemon --help` 的实际输出为准：

```bash
multica daemon status --output json   # 是否在跑、pid、所连 Server
multica daemon logs -n 200            # 最近 200 行日志（默认 50 行）
multica daemon logs -f                # 持续跟踪，复现问题时用
multica daemon restart                # 停止后再启动，新进程继承当前终端的环境
multica daemon stop
multica daemon start                  # 默认后台运行；加 --foreground 在当前终端前台运行，便于看报错
```

日志写在 CLI profile 目录下的 `daemon.log`（按大小轮转），进程崩溃的原始输出在同目录的
`daemon.err.log`；优先用 `multica daemon logs` 读，不要整份上传。`restart` 会先停掉正在运行的 daemon，
进行中的任务可能被中断；先用 `status` 和网页确认没有任务在跑。

需要日志时，只截取与当前错误相邻的最少行，并删除 token、Cookie、Authorization header、PAT、设备
代码、签名材料和带敏感 query 的 URL。不要上传完整 `config.json`、浏览器网络导出或凭据截图。

## 升级给维护者的信息

- OS/Arch、CLI version；
- 使用的 profile 名（不含 token）；
- 期望与实际的 Web/API base URL；
- 失败阶段：下载、校验、配置、登录、Workspace、daemon、Codex（安装/登录/代理）、Runtime、Cloud Auth、Repo 或渠道；
- 脱敏错误文本和复现时间；
- 是否存在仍在运行的任务，以及是否已保留原配置/回滚版本。
