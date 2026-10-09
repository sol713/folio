# Folio 主项目集成说明

## 来源及完整性

原始 MIT 运维包在导入前核验全部 17 个源文件的大小、SHA256 和完整文件清单；未发现多余或缺失文件。

- 原始源归档 SHA256：`1924277fd032bfc084dbb04783b896e6fa50044427980c5fc26a2bfa492174e6`
- 原始清单原样保留为 `upstream-manifest.json`，用于追溯原始版本，不代表当前文件的 hash
- 当前版本校验清单是 `../SOURCE-MANIFEST.json`，由 allowlist 打包脚本重新生成
- 原始模块与主项目的 MIT 许可证分别保留；参考来源仍见 `../REFERENCES.md`

集成移除了工作站个人绝对路径及一次性机器设置记录。没有带入运行数据、token、真实备份、二进制、CA bundle 或依赖缓存。

## 有意修正

1. 原始 `logical-drill` 用完整 state SHA256 相等判断恢复。真实核心会增加实例 revision 并追加恢复审计，因此这种判断会误报失败。现在精确比较所有其他状态字段，验证 revision 恰好 +1、此前审计不变（满 10000 条仅移除最老一条），并验证唯一的新 `backup.restore` / `instance` / `admin` 事件。核心仍负责 Go canonical JSON checksum 验证。输出明确提供源和恢复后两个不同的 hash
2. Compose 和 doctor 校验数字 UID、私密数据根、私密 token 与数据库可写权限，避免非 root 容器无法读取其它用户初始化的 0600 token。镜像默认 UID 65532 由生成的 Compose 当前用户 UID/GID 覆盖
3. 增加对应回归测试与可重复运行的真实 Linux 二进制集成测试；源打包 allowlist 包含新增文件

## 可重复验收

从主项目根目录：

```sh
python3 ops/scripts/check.py
python3 ops/tests/integration.py --binary dist/folio
python3 ops/scripts/package.py --manifest-only
```

`docs/test-results.json` 记录单元测试；`docs/integration-results.json` 记录集成验收的二进制 hash、检查项和可用工具。集成测试仅创建临时本地服务和数据，结束时停止所有自有服务并删除临时数据，不使用已存在的实例。

真实集成包含 health/ready、doctor、独立 token、在线导出、空实例恢复、审计/内容检查、草稿隔离、媒体字节、非空拒绝、损坏 checksum 拒绝、独立恢复演练、源不变、非 root Compose 生成、停服全目录备份/验证/恢复/重启、真实 ELF 和系统 CA bundle 的 hash 验证与 staging。

Docker CLI/daemon 在当前 Linux 环境不可用；没有真实容器构建、启动或 Docker 升级/回滚验收。Docker 生命周期仍为模拟测试。发布或实际部署前需在具备 Docker 的目标环境进行本地容器验收，且不得把这里的模拟覆盖写成生产部署成功。

## v0.2 集成覆盖

校验器接受相匹配的 version1/schema1 和 version2/schema2；恢复结果显式标明 schema 升级。版本 2 的 proposal/schedule map 必须逐项保留，不能宽泛排除。新增单元测试覆盖版本不匹配、非法 schema、严格迁移范围、工作流遗失、降级拒绝与升级报告。

当前集成脚本要求 v0.2 Linux 二进制，创建真实待审批提案和固定发布计划（暂停计划执行，并验证到期后仍保持 pending），验证其在线/离线恢复。它还验证中文/英文语言设置不改变 version JSON。可通过 `--legacy-binary /trusted/folio-v0.1` 增加真实旧版本导出→新版恢复和隔离演练；报告明确区分是否实际运行该项。报告的 binary SHA256 仅代表当次验收二进制。

隔离恢复服务对 v0.2 自动使用 `--pause-schedules` 并检查 system.info.scheduler.paused；仅按机器 version JSON 判断支持，不依赖本地化日志。旧 v0.1 省略这个标志；未知版本拒绝猜测。
