// Package adapters exposes the FOLIO operation API through thin CLI and MCP clients.
// The daemon remains the only owner of persistent state and mutation validation.
package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"folio/internal/i18n"
)

const (
	DefaultURL = "http://127.0.0.1:8080"
	// Backups and base64 media need more headroom than ordinary operation responses.
	maxPayloadBytes = 64 << 20
)

var operationName = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

// Client calls the daemon's canonical operation registry over HTTP.
// It never opens the application's database or media directory.
type Client struct {
	baseURL  string
	token    string
	http     *http.Client
	language string
}

// APIError is safe to return to an agent or print on stderr. Tokens and raw
// transport errors are deliberately excluded from its message.
type APIError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

// Operation is discovery metadata supplied by the daemon, not a second registry.
type Operation struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	ReadOnly    bool            `json:"read_only"`
	Scope       string          `json:"scope,omitempty"`
}
type Capabilities struct {
	Version    string      `json:"version"`
	Operations []Operation `json:"operations"`
}

func NewClientFromEnv() (*Client, error) {
	return NewClient(os.Getenv("FOLIO_URL"), os.Getenv("FOLIO_TOKEN"))
}

// NewClient accepts local HTTP or HTTPS URLs. Plain HTTP is restricted to
// loopback so a bearer token is not accidentally sent over an unencrypted network.
func NewClient(baseURL, token string) (*Client, error) {
	return NewClientWithLanguage(baseURL, token, i18n.Resolve(os.Getenv("FOLIO_LANG")))
}

// NewClientWithLanguage selects a locale without changing global environment.
func NewClientWithLanguage(baseURL, token, locale string) (client *Client, err error) {
	locale = i18n.Resolve(locale)
	defer func() { err = localizedError(err, locale) }()
	if baseURL == "" {
		baseURL = DefaultURL
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, &APIError{Code: "configuration_error", Message: "FOLIO_URL must be an HTTP(S) URL without credentials, a query, or a fragment"}
	}
	hostname := u.Hostname()
	ip := net.ParseIP(hostname)
	if u.Scheme == "http" && hostname != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, &APIError{Code: "configuration_error", Message: "Use HTTPS for a remote FOLIO_URL, or a loopback HTTP URL through an SSH tunnel"}
	}
	return &Client{
		baseURL: strings.TrimRight(u.String(), "/"), token: strings.TrimSpace(token), language: locale,
		http: &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}

// Call returns the daemon's JSON envelope unchanged, including operation errors.
// A non-nil error must cause the CLI to exit nonzero or an MCP isError result.
func (c *Client) Call(ctx context.Context, operation string, input json.RawMessage) (raw json.RawMessage, err error) {
	defer func() { err = localizedError(err, c.language); raw = localizedEnvelope(raw, err) }()
	if !operationName.MatchString(operation) {
		return nil, &APIError{Code: "invalid_operation", Message: "Use an operation name such as posts.list; run folio capabilities to discover valid operations"}
	}
	if len(bytes.TrimSpace(input)) == 0 {
		input = json.RawMessage(`{}`)
	}
	if err := validateObject(input); err != nil {
		return nil, err
	}
	if c.token == "" && operation != "system.capabilities" {
		return nil, &APIError{Code: "authentication_required", Message: "Set FOLIO_TOKEN to a daemon token with the required scope before calling this operation"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/op/"+operation, bytes.NewReader(input))
	if err != nil {
		return nil, &APIError{Code: "configuration_error", Message: "Could not construct the daemon request; check FOLIO_URL"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", c.language)
	req.Header.Set("User-Agent", "folio-adapter/1")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, &APIError{Code: "request_canceled", Message: "The operation request was canceled"}
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, &APIError{Code: "request_timeout", Message: "The operation timed out; read the current state before retrying a mutation"}
		}
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			return nil, &APIError{Code: "request_timeout", Message: "The daemon request timed out; read the current state before retrying a mutation"}
		}
		return nil, &APIError{Code: "connection_failed", Message: "Could not reach the FOLIO daemon. Start folio serve and verify FOLIO_URL; check the current state before retrying a mutation"}
	}
	defer resp.Body.Close()
	raw, err = io.ReadAll(io.LimitReader(resp.Body, maxPayloadBytes+1))
	if err != nil {
		return nil, &APIError{Code: "response_read_failed", Message: "Could not read the daemon response; check current state before retrying a mutation"}
	}
	if len(raw) > maxPayloadBytes {
		return nil, &APIError{Code: "response_too_large", Message: "Daemon response exceeds the 64 MiB adapter limit; request a smaller result"}
	}
	// A proxy or gateway may return non-JSON auth failures. Provide the same useful
	// authentication guidance rather than relaying HTML or request headers.
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, &APIError{Code: "authentication_failed", Message: "The daemon rejected authentication or permission. Set FOLIO_TOKEN to a token configured for this daemon with the required operation scope", HTTPStatus: resp.StatusCode}
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, &APIError{Code: "redirect_refused", Message: "The daemon returned a redirect. Set FOLIO_URL to the final trusted server URL; redirects are not followed", HTTPStatus: resp.StatusCode}
	}
	var envelope struct {
		OK    *bool           `json:"ok"`
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.OK == nil {
		return nil, &APIError{Code: "invalid_response", Message: "The server did not return a FOLIO JSON envelope; check FOLIO_URL and the daemon version", HTTPStatus: resp.StatusCode}
	}
	if !*envelope.OK || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := &APIError{Code: "operation_failed", Message: "The daemon rejected the operation; inspect the JSON error and capabilities", HTTPStatus: resp.StatusCode}
		var source struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(envelope.Error, &source) == nil {
			if source.Code != "" {
				detail.Code = source.Code
			}
			if source.Message != "" {
				detail.Message = source.Message
			}
		}
		return json.RawMessage(raw), detail
	}
	return json.RawMessage(raw), nil
}

func (c *Client) Capabilities(ctx context.Context) (caps Capabilities, err error) {
	defer func() { err = localizedError(err, c.language) }()
	raw, err := c.Call(ctx, "system.capabilities", json.RawMessage(`{}`))
	if err != nil {
		return Capabilities{}, err
	}
	var envelope struct {
		Data Capabilities `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope.Data.Operations) == 0 {
		return Capabilities{}, &APIError{Code: "invalid_capabilities", Message: "The daemon returned no usable operations; check that daemon and adapter versions match"}
	}
	return envelope.Data, nil
}

func validateObject(raw []byte) error {
	if len(raw) > maxPayloadBytes {
		return &APIError{Code: "input_too_large", Message: "Operation input exceeds the 64 MiB adapter limit"}
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return &APIError{Code: "invalid_json", Message: "Operation input must be one valid JSON object"}
	}
	return nil
}

func errorEnvelope(err error) json.RawMessage {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		apiErr = &APIError{Code: "adapter_error", Message: err.Error()}
	}
	// All error fields are serializable strings and integers.
	raw, _ := json.Marshal(struct {
		OK    bool      `json:"ok"`
		Error *APIError `json:"error"`
	}{false, apiErr})
	return raw
}

func writeJSON(w io.Writer, raw json.RawMessage) error {
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return fmt.Errorf("invalid output JSON: %w", err)
	}
	compact.WriteByte('\n')
	_, err := w.Write(compact.Bytes())
	return err
}
