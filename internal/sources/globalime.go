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
	var result []history.Series
	for _, fund := range []struct{ symbol, name, slug string }{
		{"GIBF1", "Global IME Balance Fund-I", "global-ime-balance-fund"},
		{"GBIMESY2", "Global IME Samunnat Yojana-II", "global-ime-samunnat-yojana-ii"},
	} {
		s, err := fetchScheme(ctx, client, history.Fund{Symbol: fund.symbol, Name: fund.name, Source: "globalime", Manager: "Global IME Capital", SourceURL: "https://www.globalimecapital.com/mutual-fund/" + fund.slug, HistoryURL: "https://globalimecapital.com/api/v1/public/mutual-funds/" + fund.slug}, fund.slug)
		if err != nil {
			return nil, err
		}
		result = append(result, s...)
	}
	return result, nil
}

func fetchMBL(ctx context.Context, client *http.Client) ([]history.Series, error) {
	return fetchScheme(ctx, client, history.Fund{Symbol: "MBLEF", Name: "MBL Equity Fund", Source: "machhapuchchhre", Manager: "Machhapuchchhre Capital", SourceURL: "https://mcl.com.np/mutual-funds/close/mbl-equity-fund", HistoryURL: "https://mcl.com.np/api/v1/public/mutual-funds/mbl-equity-fund"}, "mbl-equity-fund")
}

// Both managers use the same CMS. chart_data is the full history, not the paginated table.
func fetchScheme(ctx context.Context, client *http.Client, fund history.Fund, slug string) ([]history.Series, error) {
	s := history.Series{Fund: fund}
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
		if response.Status != "success" || scheme.ID <= 0 || scheme.Slug != slug || len(scheme.Rows) == 0 {
			return nil, fmt.Errorf("%s: missing or wrong scheme history", fund.Symbol)
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
