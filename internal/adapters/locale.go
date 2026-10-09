package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"folio/internal/i18n"
)

type languageKey struct{}

// WithLanguage sets this invocation's locale without mutating process-global
// environment variables. It also works for MCP startup and health checks.
func WithLanguage(ctx context.Context, locale string) context.Context {
	return context.WithValue(ctx, languageKey{}, i18n.Resolve(locale))
}

func language(ctx context.Context) string {
	if locale, ok := ctx.Value(languageKey{}).(string); ok {
		return locale
	}
	return i18n.Resolve(os.Getenv("FOLIO_LANG"))
}

// ParseGlobalLanguage removes a leading --lang VALUE or --lang=VALUE. Main may
// use it before dispatching init/serve/version/MCP and call WithLanguage. Values
// are strict for explicit flags; unsupported environment values use Resolve's
// Chinese-default fallback. Flags after the command remain command arguments.
func ParseGlobalLanguage(args []string) (string, []string, error) {
	return parseGlobalLanguage(args, i18n.Resolve(os.Getenv("FOLIO_LANG")))
}

func parseGlobalLanguage(args []string, fallback string) (string, []string, error) {
	locale := i18n.Resolve(fallback)
	seen := false
	for len(args) > 0 && (args[0] == "--lang" || strings.HasPrefix(args[0], "--lang=")) {
		if seen {
			return locale, args, localizedError(&APIError{Code: "usage_error", Message: "Use --lang only once before the command"}, locale)
		}
		seen = true
		value := ""
		if args[0] == "--lang" {
			if len(args) < 2 {
				return locale, args, localizedError(&APIError{Code: "invalid_locale", Message: "Language must be zh-CN or en; use --lang zh-CN or --lang en before the command"}, locale)
			}
			value = args[1]
			args = args[2:]
		} else {
			value = strings.TrimPrefix(args[0], "--lang=")
			args = args[1:]
		}
		if value != "zh-CN" && value != "en" {
			return locale, args, localizedError(&APIError{Code: "invalid_locale", Message: "Language must be zh-CN or en; use --lang zh-CN or --lang en before the command"}, locale)
		}
		locale = value
	}
	return locale, args, nil
}

func localizedError(err error, locale string) error {
	if err == nil {
		return nil
	}
	var api *APIError
	if errors.As(err, &api) {
		localized := *api
		localized.Message = i18n.CLIMessage(locale, api.Message)
		return &localized
	}
	message := i18n.CLIMessage(locale, err.Error())
	if message == err.Error() {
		return err
	}
	return errors.New(message)
}

// Only the human error.message changes. All other fields stay raw JSON, so
// numeric precision, details and user-authored text are preserved.
func localizedEnvelope(raw json.RawMessage, err error) json.RawMessage {
	if raw == nil || err == nil {
		return raw
	}
	var api *APIError
	if !errors.As(err, &api) {
		return raw
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil {
		return raw
	}
	var detail map[string]json.RawMessage
	if json.Unmarshal(envelope["error"], &detail) != nil || detail == nil {
		return raw
	}
	detail["message"], _ = json.Marshal(api.Message)
	envelope["error"], _ = json.Marshal(detail)
	updated, marshalErr := json.Marshal(envelope)
	if marshalErr != nil {
		return raw
	}
	return updated
}

func newClientForContext(ctx context.Context) (*Client, error) {
	return NewClientWithLanguage(os.Getenv("FOLIO_URL"), os.Getenv("FOLIO_TOKEN"), language(ctx))
}

// WriteCLIError emits one stable JSON error envelope for root-dispatch errors.
// The caller remains responsible for returning the error and exiting nonzero.
func WriteCLIError(w io.Writer, err error) error { return writeJSON(w, errorEnvelope(err)) }
