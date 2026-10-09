# 安全默认和边界

- Host 仅绑定 127.0.0.1；容器内监听所有接口以便端口映射。远程管理使用 TLS。探测工具拒绝 redirect，认证 CLI 拒绝非 loopback 明文 HTTP。
- Scratch 镜像由已核验的静态二进制构成，无 Shell。非 root UID、read-only root、drop ALL、no-new-privileges、受限 tmpfs、PID/内存/CPU 上限；没有 Docker socket、特权容器或 host network。
- 只有 `/data` 持久可写；不使用自动创建 bind 路径，避免错误路径静默产生空实例。SQLite/媒体在同一数据库，运维不读取或绑定内部表 schema。
- Compose 配置拒绝运行 UID 与数据、token、SQLite 文件所有者不一致。数据根必须 0700；SQLite 默认 0644 文件依靠私密根目录隔离，禁止 group/other 写入。token 文件必须私密，至少 32 字符。传给主 CLI 时使用环境变量，不在 argv 中传 token；截获的 stderr 不直接显示。临时 restore 请求文件 0600，结束后清理。环境 token 仍可被具有同 UID/管理员权限的进程读取，这不替代 OS 权限隔离。
- CA 信任包来自可信系统/发行渠道，校验 hash，保持对应许可证。工具不使用 insecure TLS，不下载或执行陌生仓库的软件。
- 备份清单 hash 只能检查完整性，不能认证来源；备份和清单一起被恶意替换时，仍需要独立可信摘要/签名。离线备份包含管理员 token；逻辑快照包含草稿和媒体，均需私密保存。
- 拒绝用户创建的路径符号链接；仅规范化 macOS 固定 `/tmp`/`/var` → `/private` 别名。拒绝归档穿越、链接、特殊文件、重复文件和超限内容。不在可被不可信用户改写的共享目录运行；本机路径检查不承诺抵抗同 UID 的并发恶意篡改。
- 自动工具不删除旧备份，不删除升级失败数据，不自动 rotate token，不创建外部账号、购买服务、公开推送代码或部署生产。

`FOLIO_DRAFT_TOKEN`/`FOLIO_READ_TOKEN` 属于主项目权限模型，启用时应与管理员及彼此不同，至少 32 字符。此模块不把任何 token 注入 Compose 环境，默认由数据中的 token 文件认证。若需要角色 token，可由另行审查的 secrets 注入方式设置；不能在保存的 Compose 文件写明文值。
