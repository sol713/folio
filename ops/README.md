# Folio 运维模块

独立于核心博客和 UI。只接入已确认的 `folio` CLI，不依赖数据库内部 schema。Python 3.11+ 标准库，零 pip/npm 依赖；Docker 镜像内仅保留主应用单个 Go 二进制和 CA 信任包。源码采用 MIT 许可证。

本模块位于主项目的 `ops/`。从主项目开始请阅读 [运维入口](../docs/OPERATIONS.md)。原始校验清单保存在 `docs/upstream-manifest.json`；集成修正和验证范围见 [集成说明](docs/integration.md)。

## 接口

| 项目 | 已确认契约 |
|---|---|
| 构建 | `CGO_ENABLED=0 go build -trimpath -buildvcs=false -o dist/folio ./cmd/folio` |
| 初始化 | `folio init --data ./data [--demo]`；创建 `folio.db`、0600 的 `token`；已有 token 拒绝再次初始化 |
| 服务 | `folio serve --data ./data --addr 127.0.0.1:8080`；容器内为 `0.0.0.0:8080` |
| 数据 | SQLite `folio.db` 包含文章和媒体 BLOB；运行时可能有 WAL；token 单独存储 |
| 健康 | `/healthz` 和 `/readyz`，HTTP 200 且 JSON `status=ok`；当前两者语义相同 |
| 容器探测 | `folio healthcheck --url http://127.0.0.1:8080/healthz`，无 token，3 秒超时，成功 exit 0 |
| 认证 | 主 token 文件或 `FOLIO_TOKEN`，至少 32 字符；可选读/草稿角色 token；不能向非 loopback 明文 HTTP 发送 token |
| 关闭 | SIGTERM 后 5 秒内优雅关闭；Compose 给 15 秒 |
| 逻辑备份 | `folio call backup.export --json '{}'` 返回 `ok/data`，`data` 为 `folio-backup` v2/schema2，含 state/media/proposals/schedules/hash，无 token；仍接受旧 v1/schema1 恢复 |
| 逻辑恢复 | `folio call backup.restore --file request.json`，请求 `{backup:导出的data,confirm:true}`；仅空实例接受 |

## 本机安装和容器部署

先阅读 [部署指南](docs/deployment.md)。不要从不明来源下载二进制或运行 `curl | sh`。构建主项目时禁用 VCS stamping，避免源码包缺少 Git 元数据或本机 Git 被 Xcode 许可阻塞。

```sh
python3 ops.py doctor
python3 -m unittest discover -s tests -v
```

`prepare-image` 校验二进制 SHA256、Linux ELF 架构、静态链接和显式提供的 CA 包 hash，再生成新的构建目录。`render` 输出 Compose 可直接读取的 JSON，避免额外 YAML 依赖。模板拒绝浮动镜像 tag、root 用户、1024 以下 host 端口、路径中的 Compose 插值字符和数据符号链接，并检查运行 UID 与数据、token、SQLite 文件所有者一致。数据必须由同一非 root 运行用户初始化。

## 备份与恢复

首选 [逻辑快照与演练](docs/backup-recovery.md)。在线快照由主程序事务产生，包含媒体 BLOB，恢复到先 `init` 的空实例，目标保留独立 token。v0.2 隔离演练自动使用 `--pause-schedules`；手动恢复目标也应先以该标志启动并检查计划。当前逻辑导出上限 64 MiB；单次上传 5 MiB、Markdown 1 MiB，后续以主项目限制为准。

停服全目录归档是另一条恢复路线。`offline-backup --compose` 先停止并检查容器，再复制数据库、WAL 和 token；`--stopped` 仅适用于操作者已经停止所有写入者的本机实例。归档含管理员 token，属于敏感工件，不能公开上传或混入源码包。文件权限 0600，恢复目录 0700，强制新路径，不覆盖现有实例。归档验证文件清单、大小、SHA256 和 SQLite `quick_check`，拒绝路径穿越、重复路径、链接和特殊文件。

## 升级、回滚与验证

见 [升级回滚指南](docs/upgrade-rollback.md) 和 [安全默认](docs/security.md)。自动升级只使用本机已有的固定镜像摘要；停服后备份，启动新镜像，检查 readyz。失败时恢复旧镜像与升级前完整数据；保存失败版本的数据目录供检查。手动回滚也恢复升级前数据，升级后写入会移到保存目录，不能声称保留了这些新写入。

单元测试使用真实 SQLite、临时 HTTP 服务和合成 folio 协议 fixture；Docker 状态机采用模拟驱动。另有 `python3 tests/integration.py --binary ../dist/folio` 对真实 Linux 二进制运行独立端到端恢复验收。逻辑恢复必然追加 audit 并增加实例 revision，演练验证内容一致和精确的审计变化，不要求恢复前后整个 state hash 相等。此处未进行真实容器运行验收。运行 `python3 scripts/check.py` 生成机器可读单元测试结果；`python3 scripts/package.py` 仅打包明确允许的源文件，生成校验清单，不打包任何运行 data、token、备份、二进制或凭据。

参考成熟 GitHub 项目的非 root 容器模式，并使用 Docker/SQLite 官方文档。没有拷贝或执行这些仓库的源码，链接和许可证说明保存在 [REFERENCES.md](REFERENCES.md)。
