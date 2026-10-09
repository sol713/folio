# 升级和回滚

启动自动操作前，让当前实例健康；新旧镜像都已由可信流程构建，并留存在本机 Docker。工具只接受 `sha256:…` 或 `repository@sha256:…`，不会拉取未知镜像。需要运行中的 Docker；运维脚本必须以 Compose 指定的数据 owner 执行。

```sh
python3 ops.py upgrade --compose compose.json \
  --image sha256:REPLACE_WITH_NEW_64_HEX --backup-dir /absolute/backups
```

顺序：锁定项目 → 检查固定镜像和旧服务 → 保存旧配置与候选配置 → 写状态日志 → 停服并确认 → 全目录离线备份 → 启动新镜像 → host readyz 检查 → 成功后替换当前配置。备份包括 token。日志/配置均 0600；不记录 token 值。失败的备份操作会尝试重新启动旧实例。

新镜像启动失败或 30 秒内未健康时，工具停止候选容器，确认停止，将新版本数据移至 `data.failed-<UUID>`，从备份恢复升级前数据，再启动旧镜像。保留失败数据目录，避免直接删除升级后写入或新 schema。自动回滚成功仍以非零退出码报告“upgrade failed; previous image and pre-upgrade data restored”。

成功升级后，显式回滚命令：

```sh
python3 ops.py rollback --compose compose.json
```

只允许回滚最近一次成功升级。工具核对当前 image/数据路径、旧配置和备份原始 SHA256；不匹配时拒绝停服。回滚恢复整个升级前数据，升级后的新写入不会出现在恢复实例中，它们保留在 failed 数据目录。不同版本 schema 不安全时，不能仅换回旧二进制却继续使用新数据库。

升级/回滚期间避免让写客户端产生新请求；这个最小版本未实现入口维护页或请求排空代理。健康端点为主程序当前别名检查，不意味着所有业务能力已验证。操作者在成功升级后还应验收写稿、预览、发布和媒体。

## 中断恢复

`compose.upgrade.json` 是可检查日志，状态可能为 stopping/launching/rollback_pending/backup_failed。发现未完成日志时工具拒绝下一次 upgrade，不会猜测当前数据版本。确认容器停止、检查备份 hash 和 verify-offline、保存现有 data、按备份恢复新目录、核对 previous 配置后手动启动可信旧镜像。完成恢复后保留日志，再由操作者决定清理状态。

自动回滚中的停服/恢复/旧镜像启动也可能失败。工具保留日志和数据，不能声称成功恢复。不要删除日志盲目重试；按照路径核验工件。项目操作使用本机 advisory lock，不能阻止在其它终端绕过工具直接执行 Docker 命令。

## v0.1 → v0.2

升级会把数据库 state.schema1 迁移为 schema2，以持久保存提案和发布计划。新版逻辑导出格式升级为 version2，仍可恢复 version1；ops 明确报告 `schema_upgraded`。旧 v0.1 二进制拒绝 schema2，回滚必须连同升级前数据库恢复，不能只换镜像。离线归档的容器格式仍为 version1，不重写数据库内部 schema。
