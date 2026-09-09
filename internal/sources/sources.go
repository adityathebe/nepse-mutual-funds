// Package sources contains official-site adapters. Adapters never write data and
// return an error rather than silently accepting incomplete or ambiguous responses.
package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/adityathebe/nepse-mutual-funds/internal/history"
)

type Adapter struct {
	Name  string
	Fetch func(context.Context, *http.Client) ([]history.Series, error)
}

// All lists the contributor-maintained sources in a stable execution order.
func All() []Adapter {
	return []Adapter{{"nmb", fetchNMB}, {"prabhu", fetchPrabhu}, {"siddhartha", fetchSiddhartha}, {"globalime", fetchGlobalIME}, {"machhapuchchhre", fetchMBL}, {"rbb", fetchRBB}, {"nimb", fetchNIMB}, {"himalayaninvest", fetchHLI}, {"reliable", fetchReliable}, {"citizens", fetchCitizens}, {"nepallife", fetchNepalLife}}
}

// request bounds response size and request rate; normal TLS verification stays enabled.
// POST is used only for public, read-only filters. A *string receives HTML verbatim.
func request(ctx context.Context, client *http.Client, endpoint string, form url.Values, result any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(200 * time.Millisecond):
	}
	method := http.MethodGet
	var body io.Reader
	if form != nil {
		method = http.MethodPost
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "nepse-mutual-funds/1.0 (+https://github.com/adityathebe/nepse-mutual-funds)")
	req.Header.Set("Accept", "application/json, text/html")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", endpoint, resp.StatusCode)
	}
	const limit = 16 << 20
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if len(b) > limit {
		return fmt.Errorf("%s: response exceeds 16 MiB", endpoint)
	}
	if text, ok := result.(*string); ok {
		*text = string(b)
		return nil
	}
	if err := json.Unmarshal(b, result); err != nil {
		return fmt.Errorf("%s: invalid JSON: %w", endpoint, err)
	}
	return nil
}
