package adapters

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"time"
)

// healthCheck is intentionally independent of the operation API and never sends
// FOLIO_TOKEN. It works as a scratch-container healthcheck without curl or a shell.
func healthCheck(ctx context.Context, healthURL string) (raw json.RawMessage, err error) {
	locale := language(ctx)
	defer func() { err = localizedError(err, locale) }()
	if healthURL == "" {
		base, err := NewClientWithLanguage(os.Getenv("FOLIO_URL"), "", locale)
		if err != nil {
			return nil, err
		}
		healthURL = base.baseURL + "/healthz"
	}
	client, err := NewClientWithLanguage(healthURL, "", locale)
	if err != nil {
		return nil, &APIError{Code: "configuration_error", Message: "Healthcheck requires a loopback HTTP or remote HTTPS URL without credentials, query, or fragment"}
	}
	client.http.Timeout = 3 * time.Second
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL, nil)
	if err != nil {
		return nil, &APIError{Code: "configuration_error", Message: "Could not construct the healthcheck request"}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", locale)
	resp, err := client.http.Do(req)
	if err != nil {
		return nil, &APIError{Code: "healthcheck_failed", Message: "Health endpoint is unreachable or did not respond within 3 seconds; start the daemon and check the URL"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &APIError{Code: "healthcheck_failed", Message: "Health endpoint did not return HTTP 200", HTTPStatus: resp.StatusCode}
	}
	raw, err = io.ReadAll(io.LimitReader(resp.Body, 64<<10+1))
	if err != nil || len(raw) > 64<<10 {
		return nil, &APIError{Code: "healthcheck_failed", Message: "Could not read a valid health response"}
	}
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil || data["status"] != "ok" {
		return nil, &APIError{Code: "healthcheck_failed", Message: "Health endpoint must return a JSON object with status equal to ok"}
	}
	return json.Marshal(struct {
		OK   bool           `json:"ok"`
		Data map[string]any `json:"data"`
	}{true, data})
}
