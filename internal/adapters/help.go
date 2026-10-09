package adapters

const helpTextChinese = `FOLIO — 面向智能体的内容发布工具

用法：
  folio [--lang zh-CN|en] COMMAND ...
  folio init [--data DIR] [--demo]
  folio serve [--data DIR] [--addr HOST:PORT]
  folio version
  folio healthcheck [--url http://127.0.0.1:8080/healthz]
  folio capabilities
  folio call OP [--json '{...}' | --file PATH | --file -]
  folio mcp

命令：
  init          初始化私有数据目录和所有者令牌
  serve         启动 HTTP 服务（默认地址 127.0.0.1:8080）
  version       以 JSON 输出版本信息
  healthcheck   检查 HTTP 200 和 JSON status:ok（无需令牌，3 秒超时）
  capabilities  以 JSON 输出服务的标准操作定义
  call          调用操作（省略输入时使用 {}）
  mcp           启动官方 MCP SDK 标准输入输出适配器

环境变量：
  FOLIO_LANG    人类可读输出语言：zh-CN（默认）或 en
  FOLIO_URL     服务地址（默认 http://127.0.0.1:8080）
  FOLIO_TOKEN   具有对应权限的访问令牌（capabilities 无需令牌）

可运行 folio call posts.list --json '{}' 开始使用。
使用 --file - 从标准输入读取 JSON。成功和错误响应均以 JSON 写入标准输出。
错误退出码为非零。帮助信息写入标准错误输出。
发布需要当前 expected_revision 和 confirm:true。
请另行运行 folio serve；适配器不会直接打开数据库。
JSON 键、操作名称、错误码和 MCP 结构定义不会随语言改变。
`

func cliHelp(locale string) string {
	if locale == "en" {
		return helpTextEnglish
	}
	return helpTextChinese
}
