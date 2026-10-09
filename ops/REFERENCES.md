# 设计来源与许可证

2026-10-03 查阅。没有下载、复制或执行下列仓库源码；仅借鉴成熟运维模式，所以没有把第三方代码混入 MIT 的 ops 模块。若以后复制其实现，必须保留其原 LICENSE/copyright，并记录具体 revision。

- [Gitea rootless Dockerfile](https://github.com/go-gitea/gitea/blob/main/Dockerfile.rootless)：参考非 root 用户和明确数据根设计。[原许可证](https://github.com/go-gitea/gitea/blob/main/LICENSE) 为 MIT。这里只链接，未 vendoring。
- [Docker Compose services 官方文档](https://docs.docker.com/reference/compose-file/services/)：使用 bind mount、healthcheck、read_only、cap_drop、security_opt、stop_grace_period 等公开配置规范。
- [SQLite Backup API 官方文档](https://www.sqlite.org/backup.html)：一致快照的依据；首选主应用事务导出，离线复制只在写入者停止后使用。
- [SQLite How To Corrupt An SQLite Database File](https://www.sqlite.org/howtocorrupt.html)：活动数据库及其 journal/WAL 不能随意拆开复制。

主项目 Folio 的 LICENSE 独立保留。Python、Go 标准库和 Docker 运行环境遵循各自许可证；CA 信任包由部署操作者显式提供，须保留其发行来源和许可信息。
