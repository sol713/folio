# 安装、部署、诊断

## 可信构建

在收到的主源码目录核对 LICENSE/go.mod 后构建，不需要初始化 Git：

```sh
mkdir -p dist
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o dist/folio ./cmd/folio
chmod 0555 dist/folio
./dist/folio init --data ./data
./dist/folio serve --data ./data --addr 127.0.0.1:8080
```

`init` 只报告 token 文件路径。不要把 token 内容写在 shell 参数、README 或截图里；不要公开 demo 实例。部署前读取数据目录权限，不需要自动接受 Xcode 许可。

主机诊断：`python3 ops.py doctor --data /absolute/data`。输出各工具是否可用，不打印 token 或命令 stderr。`doctor` 是报告命令，exit 0 不表示 Docker 可用；读取 `container_runtime_ready`。HTTP 探测失败 exit 1：

```sh
python3 ops.py probe --url http://127.0.0.1:8080
python3 ops.py probe --url http://127.0.0.1:8080 --ready
```

`readyz` 当前为 health 别名，不能据此声称进行了独立数据库深度检查。数据恢复额外执行 `PRAGMA quick_check`。

## Scratch 镜像

在主项目构建 Linux 目标（示例 ARM64，AMD64 将 GOARCH 改为 amd64）：

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -buildvcs=false -o /trusted/artifacts/folio-linux-arm64 ./cmd/folio
```

从可信 OS/发行环境获得 PEM CA 信任包，保留来源及发布者许可证。`prepare-image` 不下载任何包；调用者提供可信预期 hash。对自己刚构建的产物记录 hash 用于运输完整性；这不替代发布者身份验证。

```sh
python3 ops.py prepare-image \
  --binary /trusted/artifacts/folio-linux-arm64 --sha256 REPLACE_WITH_64_HEX \
  --ca-bundle /trusted/artifacts/ca-certificates.crt --ca-sha256 REPLACE_WITH_64_HEX \
  --arch arm64 --output ./dist
docker build --platform linux/arm64 -f deploy/Dockerfile -t folio:local .
docker image inspect --format '{{.Id}}' folio:local
```

`dist` 必须不存在，防止覆盖已验证产物。准备过程拒绝 Mach-O、动态 ELF 和错误架构。镜像使用 `FROM scratch`，没有下载构建器、Shell、curl 或特权 init。CA 文件支持应用的外部 HTTPS 请求；不关闭 TLS 验证。

先以运行服务的同一非 root 用户初始化本机数据，然后用 image inspect 返回的完整 `sha256:…` ID 生成 Compose：

```sh
python3 ops.py render --data /absolute/data --image sha256:REPLACE_WITH_64_HEX --output compose.json
docker compose -f compose.json config --quiet
docker compose -f compose.json up -d --no-build --pull never
docker compose -f compose.json ps
python3 ops.py probe --url http://127.0.0.1:8080 --ready
```

生成器默认采用执行者的数字 UID/GID，覆盖镜像的 `65532:65532` 默认值。运行 UID 必须拥有数据目录、token、数据库和现有 WAL/SHM；目录保持 0700，token 保持 0600（或只读 0400）。SQLite 默认 0644 文件仅可存在于 0700 私密目录内，禁止 group/other 写权限。不要以 root 初始化再期待非 root 容器能读写；如需专用服务用户，应先以该用户初始化，再以相同 UID 生成配置。不自动修改所有权。

若只有独立 `docker-compose` 可用，以它替代上述 `docker compose`。CLI 工具自动选择；项目名固定为 `folio`，同一 Docker daemon 不同时运行多个同名部署。`--port` 可改 host 端口，容器端口固定 8080。`--uid/--gid` 可显式指定容器用户；自动升级/回滚必须由该数据 owner 执行，保证恢复文件仍可被服务读取。

绑定目录必须已存在，token 0600 且至少 32 字符，folio.db 已初始化。不要使用 NFS/SMB 共享 SQLite 数据；选本机磁盘。只发布 `127.0.0.1:8080`。需要远程访问时，在另行授权的部署任务中配置 TLS 和访问控制；本模块不创建外部账户或部署生产服务器。

## 验证平台与限制

初始模块在 macOS/ARM64 通过合成协议与 SQLite 单元测试；集成版本在 Linux/AMD64 通过真实 Folio 二进制的探测、逻辑恢复、离线恢复与媒体验收。详细结果见 [集成说明](integration.md)。当前集成环境没有 Docker CLI/daemon，未构建或运行容器；升级/回滚容器状态机仅由模拟驱动覆盖。
