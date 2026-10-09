package adapters

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"

	"folio/internal/i18n"
)

const helpTextEnglish = `FOLIO — agent-native publishing

Usage:
  folio [--lang zh-CN|en] COMMAND ...
  folio init [--data DIR] [--demo]
  folio serve [--data DIR] [--addr HOST:PORT]
  folio version
  folio healthcheck [--url http://127.0.0.1:8080/healthz]
  folio capabilities
  folio call OP [--json '{...}' | --file PATH | --file -]
  folio mcp

Commands:
  init          Initialize a private data directory and owner token
  serve         Run the HTTP daemon (default 127.0.0.1:8080)
  version       Print the version as JSON
  healthcheck   Verify HTTP 200 and JSON status ok (no token; 3-second timeout)
  capabilities  Print the daemon's canonical operation schemas as JSON
  call          Call an operation (omitted input defaults to {})
  mcp           Run the official MCP SDK stdio adapter

Environment:
  FOLIO_LANG    Human output language: zh-CN (default) or en
  FOLIO_URL     Daemon URL (default http://127.0.0.1:8080)
  FOLIO_TOKEN   Scoped bearer token (not required for capabilities)

Use folio call posts.list --json '{}' to begin.
Use --file - to read JSON from stdin. Success and errors are JSON on stdout.
Error exit codes are nonzero. Help is written to stderr.
Publishing requires a current expected_revision and confirm:true.
Run folio serve separately; adapters never open the database.
`

// RunCLI executes arguments excluding argv[0]. It writes exactly one compact
// JSON envelope for a call, including failures, and returns errors to let main
// select a nonzero exit code. Help goes to stderr. MCP owns stdout when selected.
func RunCLI(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) (resultErr error) {
	locale, remaining, parseErr := parseGlobalLanguage(args, language(ctx))
	ctx = WithLanguage(ctx, locale)
	defer func() { resultErr = localizedError(resultErr, locale) }()
	if parseErr != nil {
		if e := writeJSON(stdout, errorEnvelope(parseErr)); e != nil {
			return e
		}
		return parseErr
	}
	args = remaining
	if len(args) == 0 || (len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h")) {
		_, err := io.WriteString(stderr, cliHelp(locale))
		return err
	}
	if args[0] == "mcp" {
		if len(args) != 1 {
			return &APIError{Code: "usage_error", Message: "folio mcp takes no arguments; configure FOLIO_URL and FOLIO_TOKEN in the environment"}
		}
		return runMCPWithIO(ctx, stdin, stdout)
	}
	raw, err := runCLI(ctx, args, stdin)
	err = localizedError(err, locale)
	raw = localizedEnvelope(raw, err)
	if err != nil && raw == nil {
		raw = errorEnvelope(err)
	}
	if writeErr := writeJSON(stdout, raw); writeErr != nil {
		return writeErr
	}
	return err
}

func runCLI(ctx context.Context, args []string, stdin io.Reader) (json.RawMessage, error) {
	operation := ""
	input := json.RawMessage(`{}`)
	switch args[0] {
	case "healthcheck":
		if len(args) == 1 {
			return healthCheck(ctx, "")
		}
		if len(args) == 3 && args[1] == "--url" && args[2] != "" {
			return healthCheck(ctx, args[2])
		}
		return nil, usageError(ctx, "Use folio healthcheck [--url http://127.0.0.1:8080/healthz]")
	case "capabilities":
		if len(args) != 1 {
			return nil, usageError(ctx, "folio capabilities takes no arguments")
		}
		operation = "system.capabilities"
	case "call":
		if len(args) < 2 {
			return nil, usageError(ctx, "Expected an operation: folio call OP [--json '{...}' | --file PATH]")
		}
		operation = args[1]
		if len(args) != 2 && len(args) != 4 {
			return nil, usageError(ctx, "Use one input source: --json '{...}' or --file PATH (use - for stdin)")
		}
		if len(args) == 4 {
			switch args[2] {
			case "--json":
				input = json.RawMessage(args[3])
			case "--file":
				var reader io.Reader = stdin
				if args[3] != "-" {
					file, err := os.Open(args[3])
					if err != nil {
						return nil, &APIError{Code: "input_read_failed", Message: "Could not open the --file input; verify the path and file permissions"}
					}
					defer file.Close()
					reader = file
				}
				var err error
				input, err = io.ReadAll(io.LimitReader(reader, maxPayloadBytes+1))
				if err != nil {
					return nil, &APIError{Code: "input_read_failed", Message: "Could not read the JSON input"}
				}
			default:
				return nil, usageError(ctx, "Unknown input option; use --json or --file")
			}
			if len(strings.TrimSpace(string(input))) == 0 {
				return nil, &APIError{Code: "invalid_json", Message: "The selected input source is empty; provide a JSON object such as {}"}
			}
		}
	default:
		return nil, usageError(ctx, "Unknown command; use folio capabilities, folio call OP, or folio mcp")
	}
	client, err := newClientForContext(ctx)
	if err != nil {
		return nil, err
	}
	return client.Call(ctx, operation, input)
}

func usageError(ctx context.Context, message string) error {
	locale := language(ctx)
	return &APIError{Code: "usage_error", Message: i18n.CLIFormat(locale, "%s. Run folio --help for usage", i18n.CLIMessage(locale, message))}
}
