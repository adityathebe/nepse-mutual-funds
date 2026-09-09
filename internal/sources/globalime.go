package sources

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/adityathebe/nepse-mutual-funds/internal/history"
)

// fetchGlobalIME preserves the NAV table's English Date (published_date).
// The source does not separately expose an accounting valuation date.
func fetchGlobalIME(ctx context.Context, client *http.Client) ([]history.Series, error) {
	s := history.Series{Fund: history.Fund{Symbol: "GIBF1", Name: "Global IME Balance Fund-I", Source: "globalime", Manager: "Global IME Capital", SourceURL: "https://www.globalimecapital.com/mutual-fund/global-ime-balance-fund", HistoryURL: "https://globalimecapital.com/api/v1/public/mutual-funds/global-ime-balance-fund"}}
	seen := map[history.Point]bool{}
	for _, frequency := range []string{"weekly", "monthly"} {
		var response struct {
			Status string `json:"status"`
			Data   struct {
				Scheme struct {
					ID   int    `json:"id"`
					Slug string `json:"slug"`
					Rows []struct {
						Date string `json:"published_date"`
						NAV  string `json:"value"`
						Name string `json:"name"`
					} `json:"chart_data"`
				} `json:"scheme"`
			} `json:"data"`
		}
		if err := request(ctx, client, s.Fund.HistoryURL+"?type="+frequency, nil, &response); err != nil {
			return nil, err
		}
		scheme := response.Data.Scheme
		if response.Status != "success" || scheme.ID != 6 || scheme.Slug != "global-ime-balance-fund" || len(scheme.Rows) == 0 {
			return nil, fmt.Errorf("Global IME: missing or wrong scheme history")
		}
		for _, row := range scheme.Rows {
			nav, err := strconv.ParseFloat(row.NAV, 64)
			if err != nil {
				return nil, err
			}
			p := history.Point{AsOf: row.Date, NAV: nav, Frequency: frequency, SourceLabel: row.Name}
			// Global IME also repeats exact rows; do not discard conflicting NAVs.
			if !seen[p] {
				s.History = append(s.History, p)
				seen[p] = true
			}
		}
	}
	if err := history.Normalize(s.History, time.Now()); err != nil {
		return nil, err
	}
	return []history.Series{s}, nil
}
