# 备份和恢复演练

## 首选：在线逻辑快照

由 folio 事务输出一致的文章/媒体状态和 hash。运维仅验证外层协议，不重写主程序的 Go hash 算法，也不依赖 SQLite 表结构。主程序在 restore 时验证状态及媒体 hash。逻辑备份含内容、未发布草稿和媒体，虽不含 token，仍应私密保存。

```sh
mkdir -m 700 backups
python3 ops.py logical-export --binary /trusted/folio \
  --url http://127.0.0.1:8080 --token-file /absolute/data/token \
  --output backups/logical-001.json
```

恢复目标先 `folio init --data /new/data`，然后用 `folio serve --data /new/data --addr 127.0.0.1:8081 --pause-schedules` 独立启动。v0.2 的恢复目标应暂停计划执行，先检查恢复结果和待发布任务。只接受空实例；目标保留自己生成的独立 token，不会复制源管理员 token：

```sh
python3 ops.py logical-restore --binary /trusted/folio \
  --url http://127.0.0.1:8081 --token-file /new/data/token \
  --backup backups/logical-001.json
```

自动演练创建全新的私密目录、初始化新的 token、选择 loopback 临时端口、启动可信主程序（v0.2 自动加 `--pause-schedules` 并核验 `system.info.scheduler.paused`）、恢复、再次导出并比较文章、版本、媒体、设置和其他业务状态，最后 SIGTERM 关闭演练服务。恢复操作按核心契约将实例 revision 增加 1，并追加一个 `backup.restore` 审计事件（10000 条保留上限）；演练逐项验证这一变化及此前审计记录。两个完整 state SHA256 正常情况下不同，会同时报告，不以自行重算 Go JSON hash 代替核心校验。源实例只执行 export。

```sh
python3 ops.py logical-drill --binary /trusted/folio \
  --url http://127.0.0.1:8080 --token-file /absolute/data/token \
  --workdir /absolute/new-drill-directory
```

工作目录必须不存在，演练成功或失败都保留可检查工件；其内部 token/data 不可打包进源码。业务备份能力之外，应定期人工查看恢复后页面和媒体；自动一致性校验不能替代使用者验证。

当前主程序逻辑导出最大 64 MiB；媒体 base64 会扩大体积，主机 CLI 的 JSON 编码也有内存开销。此工具拒绝超过 64 MiB 的逻辑文件，但不提供流式导出，接近上限时应评估更大版本的备份能力。主程序 upload 单次 5 MiB、Markdown 1 MiB，后续以集成主项目文档为准。

## 额外路线：停服全目录备份

不能复制活动的 `folio.db` 后丢弃其 WAL。使用此工具的 Compose 路线可证明容器已停：

```sh
python3 ops.py offline-backup --compose compose.json \
  --data /absolute/data --output backups/offline-001.tar.gz
```

该命令停止 blog、检查 Docker Running=false、完整复制数据到独立临时目录、检查 SQLite、生成逐文件清单，最后重新启动原实例。不能证明其它进程也停止：部署 operator 须确保只有一个写入者使用此根目录。

本机非 Compose 服务先 SIGTERM 并等待完全退出，再使用 `--stopped`。这个标志是操作者的停服声明，不是进程检测能力：

```sh
python3 ops.py offline-backup --stopped --data /absolute/data \
  --output backups/offline-001.tar.gz
python3 ops.py verify-offline --backup backups/offline-001.tar.gz
python3 ops.py offline-restore --backup backups/offline-001.tar.gz \
  --target /absolute/new-restored-data
```

完整归档包含 token、数据库和可能存在的 WAL/SHM，以及未来本地媒体目录。恢复目标必须不存在；恢复副本会继承源 token。不要把同一管理员 token 同时用在两个可被他人访问的实例；需要独立权限时选逻辑恢复。

归档文件 0600，恢复目录 0700/文件 0600；SHA256 清单检验传输损坏，不等同于数字签名。把 `sha256` 摘要存在独立受信任位置，在恢复前核对归档原始 hash。默认解压总大小上限 4 GiB；大实例需先评估容量并调整受审查的限制。恢复只验证 SQLite 可读性和文件完整性；仍需主程序启动与页面/媒体验收。

## 保存策略

建议保留多个时间点、至少一个独立磁盘副本，每月演练并保存状态 hash/结果。此模块不自动删除旧备份或失败数据，不创建云凭据，不购买存储，不上传敏感备份。操作者依据容量和保密要求决定加密、访问控制及保留期。

## v0.2 schema 升级和工作流

v0.2 导出 `folio-backup` version2、state.schema2，备份完整保留 proposals 与 schedules（包括审批状态、候选内容、固定发布快照和时刻）。恢复器仍接受 version1/schema1；成功后输出 `source_schema`、`schema`、`schema_upgraded`，明确标明 1→2 升级。比较器只允许这一次 schema 变化和新出现的空工作流映射；文章、媒体、设置或已有工作流内容不能被忽略。两端均 v0.2 时所有工作流字段逐项相同。核心负责旧版本 checksum 验证与迁移。

离线归档格式仍为 `folio-offline` version1；它保存原始数据库文件，和逻辑备份版本是独立概念。已经升级到 schema2 的数据库不能交给 v0.1 二进制继续写入；安全回滚使用升级前完整离线备份。旧二进制拒绝新 schema，以免静默丢失工作流字段。

v0.2 正常服务会在启动及随后约每秒扫描到期计划；恢复的过期计划可能很快发布。手动恢复前必须以 `serve --pause-schedules` 启动目标，检查 `system.info.scheduler.paused=true` 后再恢复和审核。隔离演练自动暂停并验证这个状态；不会静默恢复执行。审核后是否停止暂停服务并正常启动，由操作者明确决定。v0.1 无计划功能，演练不会传递不支持的标志。

机器 JSON 字段不随 `FOLIO_LANG` 或全局 `--lang zh-CN|en` 变化。测试提取启动信息中的 loopback URL，不依赖中文或英文文案。
