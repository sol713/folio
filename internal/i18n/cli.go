package i18n

import "fmt"

// CLIMessage translates adapter-owned human text. Machine keys, operation names,
// error codes, JSON Schema and caller-provided content are deliberately untouched.
func CLIMessage(locale, english string) string {
	if Resolve(locale) == "en" {
		return english
	}
	if translated, ok := cliChinese[english]; ok {
		return translated
	}
	return Message(locale, english)
}

func CLIFormat(locale, template string, args ...any) string {
	return fmt.Sprintf(CLIMessage(locale, template), args...)
}

var cliChinese = map[string]string{
	"FOLIO_URL must be an HTTP(S) URL without credentials, a query, or a fragment":                                                              "FOLIO_URL 必须是 HTTP(S) 地址，且不能包含凭据、查询参数或片段",
	"Use HTTPS for a remote FOLIO_URL, or a loopback HTTP URL through an SSH tunnel":                                                            "远程 FOLIO_URL 请使用 HTTPS，或通过 SSH 隧道访问本机回环 HTTP 地址",
	"Use an operation name such as posts.list; run folio capabilities to discover valid operations":                                             "请使用 posts.list 等操作名称；运行 folio capabilities 查看可用操作",
	"Set FOLIO_TOKEN to a daemon token with the required scope before calling this operation":                                                   "调用此操作前，请将 FOLIO_TOKEN 设置为具有所需权限的服务令牌",
	"Could not construct the daemon request; check FOLIO_URL":                                                                                   "无法创建服务请求，请检查 FOLIO_URL",
	"The operation request was canceled":                                                                                                        "操作请求已取消",
	"The operation timed out; read the current state before retrying a mutation":                                                                "操作超时；重试修改操作前，请先读取当前状态",
	"The daemon request timed out; read the current state before retrying a mutation":                                                           "服务请求超时；重试修改操作前，请先读取当前状态",
	"Could not reach the FOLIO daemon. Start folio serve and verify FOLIO_URL; check the current state before retrying a mutation":              "无法连接 FOLIO 服务。请运行 folio serve 并检查 FOLIO_URL；重试修改操作前，请先检查当前状态",
	"Could not read the daemon response; check current state before retrying a mutation":                                                        "无法读取服务响应；重试修改操作前，请先检查当前状态",
	"Daemon response exceeds the 64 MiB adapter limit; request a smaller result":                                                                "服务响应超过适配器的 64 MiB 限制，请缩小查询结果范围",
	"The daemon rejected authentication or permission. Set FOLIO_TOKEN to a token configured for this daemon with the required operation scope": "服务拒绝了身份验证或权限请求。请将 FOLIO_TOKEN 设置为此服务已配置且具有所需操作权限的令牌",
	"The daemon returned a redirect. Set FOLIO_URL to the final trusted server URL; redirects are not followed":                                 "服务返回了重定向。请将 FOLIO_URL 设置为最终可信服务地址；适配器不会自动跟随重定向",
	"The server did not return a FOLIO JSON envelope; check FOLIO_URL and the daemon version":                                                   "服务未返回 FOLIO JSON 响应，请检查 FOLIO_URL 和服务版本",
	"The daemon rejected the operation; inspect the JSON error and capabilities":                                                                "服务拒绝了此操作，请查看 JSON 错误信息和可用操作定义",
	"The daemon returned no usable operations; check that daemon and adapter versions match":                                                    "服务未返回可用操作，请检查服务与适配器版本是否兼容",
	"Operation input exceeds the 64 MiB adapter limit":                                                                                          "操作输入超过适配器的 64 MiB 限制",
	"Operation input must be one valid JSON object":                                                                                             "操作输入必须是一个有效的 JSON 对象",
	"folio mcp takes no arguments; configure FOLIO_URL and FOLIO_TOKEN in the environment":                                                      "folio mcp 不接受额外参数；请通过环境变量配置 FOLIO_URL 和 FOLIO_TOKEN",
	"Use folio healthcheck [--url http://127.0.0.1:8080/healthz]":                                                                               "用法：folio healthcheck [--url http://127.0.0.1:8080/healthz]",
	"folio capabilities takes no arguments":                                                                                                     "folio capabilities 不接受额外参数",
	"Expected an operation: folio call OP [--json '{...}' | --file PATH]":                                                                       "请指定操作，用法：folio call OP [--json '{...}' | --file PATH]",
	"Use one input source: --json '{...}' or --file PATH (use - for stdin)":                                                                     "请选择一种输入来源：--json '{...}' 或 --file PATH（使用 - 从标准输入读取）",
	"Could not open the --file input; verify the path and file permissions":                                                                     "无法打开 --file 指定的文件，请检查路径和文件权限",
	"Could not read the JSON input":                                                                                                             "无法读取 JSON 输入",
	"Unknown input option; use --json or --file":                                                                                                "未知输入选项，请使用 --json 或 --file",
	"The selected input source is empty; provide a JSON object such as {}":                                                                      "所选输入内容为空，请提供 {} 等 JSON 对象",
	"Unknown command; use folio capabilities, folio call OP, or folio mcp":                                                                      "未知命令，请使用 folio capabilities、folio call OP 或 folio mcp",
	"%s. Run folio --help for usage":                                                                                                            "%s。运行 folio --help 查看用法",
	"Language must be zh-CN or en; use --lang zh-CN or --lang en before the command":                                                            "语言必须是 zh-CN 或 en；请在命令前使用 --lang zh-CN 或 --lang en",
	"Use --lang only once before the command":                                                                                                   "请仅在命令前指定一次 --lang",
	"Healthcheck requires a loopback HTTP or remote HTTPS URL without credentials, query, or fragment":                                          "健康检查需要本机回环 HTTP 或远程 HTTPS 地址，且不能包含凭据、查询参数或片段",
	"Could not construct the healthcheck request":                                                                                               "无法创建健康检查请求",
	"Health endpoint is unreachable or did not respond within 3 seconds; start the daemon and check the URL":                                    "无法连接健康检查端点，或端点未在 3 秒内响应；请启动服务并检查地址",
	"Health endpoint did not return HTTP 200":                                                                                                   "健康检查端点未返回 HTTP 200",
	"Could not read a valid health response":                                                                                                    "无法读取有效的健康检查响应",
	"Health endpoint must return a JSON object with status equal to ok":                                                                         "健康检查端点必须返回 JSON 对象，且 status 的值为 ok",
	"Invalid operation name in daemon capabilities":                                                                                             "服务操作定义中包含无效的操作名称",
	"Invalid or duplicate MCP tool name in daemon capabilities: %s":                                                                             "服务操作定义中的 MCP 工具名称无效或重复：%s",
	"Operation %s has no object input schema":                                                                                                   "操作 %s 缺少对象类型的输入结构定义",
}
