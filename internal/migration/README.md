# Folio Markdown migration

纯 Go 1.25、仅标准库的 Markdown 批量导入导出引擎，现已集成到 FOLIO v0.2。此包自身没有数据库、HTTP 客户端、凭据、渲染器或发布实现；`internal/core/migration.go` 负责认证边界内的规划／执行／导出集成，所有单项写入都经 `Caller` 调用既有私有草稿操作。

面向使用者的实际 API、CLI、MCP 和 Studio 工作流见 [迁移指南](../../docs/MIGRATION.md)。

## 集成使用与检查

在仓库根目录运行：

```sh
go test ./internal/migration ./internal/core ./internal/server ./internal/adapters
go test -race ./...
go vet ./...
folio capabilities
folio call migration.plan --file migration-input.json
```

已运行的 daemon 提供 `migration.plan`、`migration.apply`、`migration.export` 三个 admin 操作；CLI 使用 `folio call`，MCP 对应下划线工具名，Studio 在 `/studio/migration`。当前仓库不提供独立的 `folio-migrate-plan` 命令。

`migration.plan` 接收文件内容与安全相对路径，返回冻结计划、完整性哈希、目标实例身份和诊断；不读取服务器本机目录。`migration.apply` 需要原计划、准确目标实例和明确确认，只创建／替换私有草稿。产品集成限制为 200 文件、8 MiB 源输入，比本包底层上限更严格。

`conflict:error` 默认保留已有文章并报告阻塞；`skip` 明确跳过匹配 slug；`update` 明确替换其全部草稿字段并固定 `expected_revision`，不会合并正文或改动在线快照。部分成功保留，恢复时向同一实例重放同一冻结计划，不自动回滚或发布。完整请求样例及结果判定见上方迁移指南。

## 包接口

```go
Parse(sourcePath string, content []byte) (Document, error)
Plan(documents []Document, existing []Existing, options Options) (ImportPlan, error)
BuildPlan(documents []Document, existing []Existing, options Options) (ImportPlan, error)
PlanDirectory(directory string, existing []Existing, options Options) (ImportPlan, error)
ScanDirectory(directory string) ([]Document, []Diagnostic, error)
ValidatePlan(plan ImportPlan) error
WithDiagnostics(plan ImportPlan, issues []Diagnostic) ImportPlan
Apply(ctx context.Context, caller Caller, plan ImportPlan, options ApplyOptions) (Report, error)
Export(document Document) ([]byte, error)
BuildExport(documents []Document) (ExportBundle, error)
DocumentsFromExport(bundle ExportBundle) ([]Document, error)
WriteExportNewDir(directory string, bundle ExportBundle) error
```

`BuildPlan` 是 `Plan` 的同签名别名；返回类型叫 `ImportPlan`，避免 Go 同名类型/函数冲突。内存入口适合 HTTP/MCP：传文件内容，不接收客户端任意本机目录路径。目录读取和写出只能由本地 CLI 执行。

数据结构、JSON 样例见 [integration.md](docs/integration.md) 和 [sample-plan.json](docs/sample-plan.json)。确定性哈希不包含墙钟时间或本机绝对路径。计划对输入 slices/maps 做深拷贝；调用方必须在规划期间自行同步输入，不得并发改写它们。

## frontmatter 兼容子集

支持文件开头 `---` YAML 或 `+++` TOML；支持 UTF-8 中文、开头 BOM、CRLF/CR 标准化为 LF，保留正文及其末尾是否有换行。没有 frontmatter 时，从首个代码围栏之外的 H1 或文件名推断标题。

| 字段 | 行为 |
| --- | --- |
| `title` | 文本；1–200 Unicode 字符 |
| `slug` | 小写 ASCII 单词与单连字符；最长 160 字节 |
| `description` / `summary` / `excerpt` | 映射 `excerpt`；多个非空值必须一致，最长 2000 字节 |
| `tags` | 文本数组或 YAML block list；保序去重，最多 20 项，每项 1–80 字节 |
| `category` / `categories` | 单一分类；categories 必须恰好 1 项，别名值必须一致 |
| `cover` | 仅已存在的 `/media/` 本地引用；不验证/读取其资源，不复制附件 |
| `featured` | 严格 `true` / `false` |
| `date` | 内容源日期，规范化 UTC；仅保留元数据，不设置创建时间 |
| `publishDate` / `publish_date` | 与 date 分开保留；不设置发布时间、不预约发布 |
| `draft` | 源值保留，缺省 true；即使 false，也只保存私有草稿 |
| 其他平坦字段 | 原字段 literal 留在 `Extra`，产生警告，不进入文章写请求 |

日期接受 RFC3339（包括小数秒）、RFC3339 无冒号偏移、无时区 `YYYY-MM-DDTHH:MM:SS`、`YYYY-MM-DD`、`02 Jan 2006`；无时区一律 UTC。不会使用操作系统当前时区来猜日期。

支持 YAML 单/双引号、plain 文本、flat text arrays、block lists 和两空格缩进的 `|` / `|-` / `>` / `>-` block scalar；支持 TOML 单/双引号字符串、数组、布尔、bare 日期。YAML plain 值按文本处理，只有 true/false 识别为布尔；数组中的布尔须加引号才能成为标签。它不是完整 YAML/TOML 解析器。

不支持 JSON frontmatter、嵌套 maps/tables、anchors/aliases/类型标签、复杂数组、TOML 多行字符串、YAML 任意缩进/复杂折叠规则。JSON 开头会作为普通 Markdown 正文，不声称其元数据已解析。无法安全解析的 frontmatter 会产生可恢复文件错误，不会默默执行。Hugo `cascade`、模板、参数嵌套和 URL aliases 均不会导入功能。

slug 缺省从文件名提取 ASCII 字符，`index.md` 从上级目录名推断；无 ASCII 时使用 `post-` 加相对路径 SHA-256 前 12 字符。不会自动 transliterate 中文。不同文件的同 slug 和同 source_path 都会阻塞，必须修复/显式选择目标。

## 预检、执行与恢复

1. 读取可信 existing 快照；HTTP/MCP 必须验证 token 和目标实例。快照应包含 trash/保留 slug，否则 core 最终仍可能报告冲突。
2. Parse/Plan，检查 `Diagnostics`、源字段警告、目标 ID 和 expected_revision。存在任何诊断时默认不能 Apply。
3. 把确认绑定到准确 `plan.ID`，保留完整冻结计划，不在执行前从变动文件重新拼请求。
4. 通过正常授权的 `Caller` 执行每项 `posts.create` / `posts.update`。报告每项 applied/skipped/failed/pending；失败默认停止后续项，显式 `ContinueOnError` 才继续。
5. 部分成功不会自动撤销、删除或发布。保存报告，对同一实例重放同一计划、同一字节请求；core 的持久幂等缓存返回已提交回执。不能在恢复前清空幂等缓存或恢复另一个实例备份。

这不是整批原子事务。成功项已提交，即使后续失败也会保留。返回错误或无效回执可能意味着操作已提交，不代表“没有写入”。错误报告不包含底层 adapter 原始错误，避免泄露 bearer token；adapter 可以在其正常安全诊断通道处理原因。

Core 保持单项 slug 唯一约束、CAS、幂等键持久化和参数 fingerprint。已集成的包装层校验调用者角色、目标实例和冻结计划哈希，并通过 `WithDiagnostics` 将源文件／实例预检诊断封入计划校验和；完整的被拒绝预览无法在执行时偷偷跳过失败项。计划 SHA 是完整性校验，不是签名或权限凭证。`Caller` 返回未包装的操作 data，比如 `{"post":{"id":"...","slug":"...","revision":1}}`。

## 导出与附件

`Export` 输出固定字段顺序、JSON-compatible YAML 引号、LF 的 Markdown，始终写 `draft: true`，防止文件迁入其他博客时误上线。源 draft 状态、date/publishDate、未知元数据、warnings、content_hash 保留在 bundle 的 `manifest.json`。`DocumentsFromExport` 检验文件 SHA、字节数、内容与元数据对应关系，再恢复完整文档；只重新 Parse `.md` 无法恢复清单中的未知字段。

目录导出只接受新目录，0700/0600，拒绝符号链接、路径穿越和覆盖。发生 I/O 错误时保留部分目录供检查；成功清单最后写出，不自动递归删除。没有备份全实例、修订历史、令牌或媒体数据的含义，完整灾难恢复仍使用既有 ops/backup 工具。

Markdown 普通链接、相对图片、`[[wiki links]]`、`![[embeds]]` 原样保留。图片引用产生警告，不复制、不上传、不改写，不自取任何远程 URL；正文链接不作为文件系统路径执行。附件应在另一个受控媒体迁移流程中处理。

## 安全限制与测试

单文件最多 1 MiB Markdown + 64 KiB frontmatter，整批最多 1000 文档、64 MiB 文档/输入，最多扫描 10000 文件系统条目。拒绝非法 UTF-8、NUL、重复键、不安全 source_path、symlink、非普通输入文件；目录访问使用 Go `os.Root` 做边界约束，加 Lstat/SameFile 检查。macOS 仅规范化固定系统 `/tmp` 和 `/var` 别名，拒绝用户自建 symlink。输入目录应由可信本地用户控制；不声称防御恶意挂载点或设备节点的特权攻击。

测试覆盖实际临时目录、符号链接、UTF-8 fixtures、checksum/manifest、冲突、CAS、深拷贝、冻结计划重放和提交后丢失回执。Apply 的测试 adapter 模拟现有 core 幂等契约；真实 Folio 集成由云端包装层测试。源码来源与许可证见 [REFERENCES.md](REFERENCES.md)。
