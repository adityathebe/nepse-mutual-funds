// Package sources contains official-site adapters. Adapters never write data and
// return an error rather than silently accepting incomplete or ambiguous responses.
package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
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

type httpStatusError struct {
	endpoint string
	status   int
}

func (err *httpStatusError) Error() string {
	return fmt.Sprintf("%s: HTTP %d", err.endpoint, err.status)
}

// All lists the contributor-maintained sources in a stable execution order.
func All() []Adapter {
	return []Adapter{{"nmb", fetchNMB}, {"prabhu", fetchPrabhu}, {"siddhartha", fetchSiddhartha}, {"globalime", fetchGlobalIME}, {"machhapuchchhre", fetchMBL}, {"rbb", fetchRBB}, {"nimb", fetchNIMB}, {"himalayaninvest", fetchHLI}, {"reliable", fetchReliable}, {"citizens", fetchCitizens}, {"nepallife", fetchNepalLife}, {"garima", fetchGarima}, {"muktinath", fetchMuktinath}, {"kumari", fetchKumari}, {"sanima", fetchSanima}, {"lscapital", fetchLSCapital}, {"himalayan", fetchHimalayan}, {"nabil", fetchNabil}, {"nicasia", fetchNICAsia}}
}

// request bounds response size and request rate; normal TLS verification stays enabled.
// POST is used only for public, read-only filters. A *string receives HTML verbatim.
//
// The official sites intermittently drop connections, fail DNS resolution, or answer
// 202 and 5xx before serving a payload, which previously discarded a whole source for
// the day. Transient failures are retried with exponential backoff; a decoded response
// is never retried, so a retry can only follow an attempt that produced no body.
func request(ctx context.Context, client *http.Client, endpoint string, form url.Values, result any) error {
	const attempts = 3
	backoff := time.Second
	for attempt := 1; ; attempt++ {
		transient, err := requestOnce(ctx, client, endpoint, form, result)
		if err == nil || !transient || attempt == attempts {
			return err
		}
		log.Printf("retrying %s in %s after attempt %d/%d: %v", endpoint, backoff, attempt, attempts, err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
}

// requestOnce performs a single attempt and reports whether the failure is transient.
func requestOnce(ctx context.Context, client *http.Client, endpoint string, form url.Values, result any) (bool, error) {
	select {
	case <-ctx.Done():
		return false, ctx.Err()
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
		return false, err
	}
	req.Header.Set("User-Agent", "nepse-mutual-funds/1.0 (+https://github.com/adityathebe/nepse-mutual-funds)")
	req.Header.Set("Accept", "application/json, text/html")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := client.Do(req)
	if err != nil {
		// Dial, TLS, and DNS failures are worth another attempt; an expired or
		// cancelled context belongs to the caller and is not retried.
		return ctx.Err() == nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return transientStatus(resp.StatusCode), &httpStatusError{endpoint: endpoint, status: resp.StatusCode}
	}
	const limit = 16 << 20
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return ctx.Err() == nil, err
	}
	if len(b) > limit {
		return false, fmt.Errorf("%s: response exceeds 16 MiB", endpoint)
	}
	if text, ok := result.(*string); ok {
		*text = string(b)
		return false, nil
	}
	if err := json.Unmarshal(b, result); err != nil {
		return false, fmt.Errorf("%s: invalid JSON: %w", endpoint, err)
	}
	return false, nil
}

// transientStatus reports whether a status is worth retrying. The WordPress endpoints
// answer 202 while they prepare the NAV payload, and the sites return 429 and 5xx
// under load. Every other non-200 status reflects the request itself.
func transientStatus(status int) bool {
	switch status {
	case http.StatusAccepted, http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true
	}
	return status >= 500
}
