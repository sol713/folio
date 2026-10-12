# Process crash recovery / 进程崩溃恢复

## English

This test-only gate fills an evidence gap: the existing daemon/workflow restart
tests send SIGTERM, and their `.kill()` calls are timeout cleanup. Docker also
checks a graceful restart. Those checks remain useful but did not demonstrate
recovery after an asserted, real SIGKILL. Product code, SQLite configuration,
dependencies, operation count and permission boundaries are unchanged.

Reproduce on POSIX with Python's standard library and a real Folio binary:

```sh
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o dist/folio ./cmd/folio
python3 scripts/crash_recovery_e2e.py --binary dist/folio --report /tmp/folio-crash-recovery.json
```

The script reuses `WorkflowHarness`, initializes only disposable instances,
binds to ephemeral loopback ports, waits for health, and kills only its own
live child. Each crash requires `wait()` to return `-SIGKILL` (`-9` on Linux)
and a nonempty WAL. Restart uses the exact original data directory with
`initialize=False`; database device/inode and persisted instance identity must
remain unchanged. No backup restore or fixture reconstruction supplies the
positive recovery result.

1. **Acknowledged writes and exact retries.** Real HTTP creates private R1 and
   saves R2 with separate retry keys. After SIGKILL, both acknowledgements must
   replay exactly: the old create receipt is still R1, the update receipt is R2.
   The current full post and both historical snapshots remain exact, with one
   post, no extra history, no changed instance revision or audit entries, and no
   publication through the checked public APIs, search, RSS, sitemap or index.
2. **Pinned approval and terminal execution.** With execution temporarily paused,
   the owner confirms a future R1 schedule, then writes a private R2. The pending
   approval and newer draft are acknowledged before SIGKILL. A bounded offline
   wait makes the job overdue; an ordinary unpaused restart runs the actual
   scheduler. Only all eight approved R1 content fields become live. R2 and its
   saved timestamp/history remain private and intact. Retrying the exact schedule
   request returns its original pending receipt while the real job stays terminal.
   There is exactly one scheduler publication event for both the post and job.
   A second SIGKILL after acknowledged execution, followed by the same-database
   restart and polling across a full worker interval, must leave the entire
   terminal job, instance revision and audit unchanged. Public API, search,
   article/index SSR, RSS and sitemap must not expose R2 or its URL.

Schedule creation gets 15 seconds of future lead time: pausing execution does
not suspend create-time validation, so the lead exceeds the HTTP client's
five-second request budget. Offline expiry uses short real-clock polls and a
monotonic cap of remaining lead time plus five seconds; terminal status is
polled for at most 15 seconds after restart. There is no long fixed sleep,
clock adjustment or replacement of the originally approved time.

Two **negative controls** exercise those same recovery assertions through real
daemons. After the first relevant SIGKILL, each control copies the entire stopped
temporary instance, including WAL and its temporary credential. Only the clone
is changed using standard-library SQLite: one loses exactly the create receipt;
the other substitutes private R2 for the pending R1 snapshot/revision. Both must
start healthily. The first retains the correct R2/history but replay returns
HTTP 409, which the exact-receipt checker rejects. The second actually executes
its altered job, which the pinned-version checker rejects. Startup failures,
timeouts and unrelated assertions cannot count as successful negative controls.
These deliberate corruptions are test sensitivity evidence, not observations
of Folio losing data or changing approval during a real crash.

The report records three actual SIGKILL exits, three same-database restarts,
both invariant sets and both specific negative-control rejections. Failure keeps
a failure summary and sanitized daemon diagnostics beside the requested report.
Owned children are cleaned and temporary databases/credentials removed; neither
is a test artifact. Bearer values must not appear in captured outputs or logs.
Both Go versions run this gate in CI; all existing gates remain enabled.

**Limit:** this proves recovery of acknowledged transactions after process
termination while the kernel, filesystem and their caches stay alive. It does
not simulate power loss, an OS crash, disk/controller failure, torn writes,
storage corruption, concurrent writer death or every possible interruption
during an unacknowledged transaction. It establishes no broader hardware
durability guarantee. No daemon failpoint or reduced SQLite safety setting is
used, and no existing user instance, external provider or public deployment is
accessed. Runtime reports and raw verification logs are kept outside source.

## 中文

本轮只补测试证据。原来的真实守护进程、工作流和 Docker 重启使用 SIGTERM；
`.kill()` 是超时清理后备路径，没有把真实 SIGKILL 及其恢复结果列为验收断言。
产品源码、SQLite 配置、依赖、36 个操作和权限边界均不变，原有检查全部保留。
上面的命令可在 POSIX 环境复现；仅需真实 Folio 二进制和 Python 标准库。

测试复用 `WorkflowHarness`，只创建临时实例、随机回环端口和自有子进程。
强杀前确认进程存活且 WAL 非空，强杀后必须取得 `-SIGKILL` 退出码；重启明确
使用同一个数据库目录且不重新初始化，核对设备号、inode 和持久化实例身份。
正向恢复没有导入备份，也没有重新写入测试稿件来伪造数据保留。

- 已确认写入：HTTP 创建私有 R1、保存 R2，分别带重试键。SIGKILL 后同载荷重试，
  必须返回各自原始回执；当前完整 R2、完整 R1/R2 历史、唯一稿件、实例版本和
  审计均保持，公开 API、搜索、RSS、站点地图和首页不能出现私有稿件。
- 固定计划：暂停执行时由 Owner 明确批准未来发布 R1，再保存私有 R2；计划和
  R2 均确认成功后强杀。离线有界等待到逾期，正常重启运行真实调度器，只发布
  R1 的全部八个内容字段，R2 的内容、保存时间和历史不变。同载荷重试仍返回
  原始待执行回执，实际计划保持已发布终态。稿件和计划各只有一次调度发布审计。
  执行确认后再强杀一次，原库重启并跨过一个真实工作周期轮询，完整终态、实例
  版本和审计不得变化；公开 API、搜索、文章/首页 SSR、RSS 和站点地图不泄露
  R2 内容或 URL。

创建计划预留 15 秒未来余量：暂停执行不会暂停创建时的未来时间校验，余量须
超过 HTTP 客户端的 5 秒请求预算。强杀后用短轮询等待原定真实到期时间，单调
时钟限定最多剩余余量加 5 秒；重启后的终态轮询最多 15 秒。没有长固定等待、
修改时钟或替换原批准时间。

两个负控各自在相关进程强杀后，复制完整已停止的临时实例（包括 WAL 和临时
凭据），仅修改独立副本：删除一个创建回执，或把固定的 R1 快照和版本改成 R2。
副本必须健康启动。前者完整 R2/历史保持，但重试得到 HTTP 409，必须被同一个
精确回执检查拒绝；后者实际执行篡改后的计划，必须被同一个固定版本检查拒绝。
启动失败、超时或无关断言不能充当负控成功。这是断言有效性的故意故障注入，
不是发现 Folio 在真实崩溃中丢数据或漂移计划。

JSON 记录三次实际强杀、三次原库重启、两组恢复不变量和两个具体负控拒绝。
失败时在报告旁保留错误摘要和去凭据的守护进程日志；只清理自有进程和临时目录，
数据库、凭据不成为报告附件，也不进入源码。CI 的两个 Go 版本均运行此检查。

**证明范围：进程终止后，内核、文件系统和缓存仍存活时的已确认事务恢复。**
没有模拟断电、操作系统崩溃、磁盘或控制器损坏、撕裂写入、存储损坏、多写进程
死亡或未确认事务的所有中断点，不能据此承诺断电或硬件故障耐久性。没有增加
守护进程故障注入接口，也没有调低 SQLite 安全配置；不接触用户实例、外部模型
或公网服务。运行报告和原始验证日志保留在源码目录以外。
