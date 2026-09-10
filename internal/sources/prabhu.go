package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/adityathebe/nepse-mutual-funds/internal/history"
)

// fetchPrabhu decodes the complete chart arrays, including monthly period labels.
func fetchPrabhu(ctx context.Context, client *http.Client) ([]history.Series, error) {
	var result []history.Series
	for _, fund := range []struct{ symbol, name string }{{"PSF", "Prabhu Select Fund"}, {"PRSF", "Prabhu Smart Fund"}} {
		series, err := fetchFlowvity(ctx, client, history.Fund{Symbol: fund.symbol, Name: fund.name, Source: "prabhu", Manager: "Prabhu Capital", SourceURL: "https://www.prabhucapital.com/mutual-fund?tabKey=" + fund.symbol, HistoryURL: "https://www.prabhucapital.com/adminapi/v1/public/hist-nav?ticker=" + fund.symbol})
		if err != nil {
			return nil, err
		}
		result = append(result, series...)
	}
	return result, nil
}

func fetchHimalayan(ctx context.Context, client *http.Client) ([]history.Series, error) {
	return fetchFlowvity(ctx, client, history.Fund{Symbol: "H8020", Name: "Himalayan 80-20", Source: "himalayan", Manager: "Himalayan Capital", SourceURL: "https://himalayancapital.com/nav-details", HistoryURL: "https://flowvity.himalayancapital.com/adminapi/v1/public/hist-nav?ticker=H8020"})
}

// Prabhu and Himalayan publish the same Flowvity chart contract.
func fetchFlowvity(ctx context.Context, client *http.Client, fund history.Fund) ([]history.Series, error) {
	s := history.Series{Fund: fund}
	var response struct {
		Status int                        `json:"status"`
		Data   map[string]json.RawMessage `json:"data"`
	}
	if err := request(ctx, client, s.Fund.HistoryURL, nil, &response); err != nil {
		return nil, err
	}
	if response.Status != 200 {
		return nil, fmt.Errorf("Prabhu: status %d", response.Status)
	}
	for _, frequency := range []string{"daily", "weekly", "monthly"} {
		var rows [][]json.RawMessage
		if err := json.Unmarshal(response.Data[frequency+"NavData"], &rows); err != nil {
			return nil, err
		}
		if rows == nil || (frequency != "daily" && len(rows) == 0) {
			return nil, fmt.Errorf("Prabhu: missing %s history", frequency)
		}
		for _, row := range rows {
			expected := 3
			if frequency == "monthly" {
				expected = 4
			}
			if len(row) != expected {
				return nil, fmt.Errorf("Prabhu: unexpected row format")
			}
			p := history.Point{Frequency: frequency}
			var name string
			if err := json.Unmarshal(row[0], &p.AsOf); err != nil {
				return nil, err
			}
			if err := json.Unmarshal(row[1], &p.NAV); err != nil {
				return nil, err
			}
			if err := json.Unmarshal(row[2], &name); err != nil {
				return nil, err
			}
			if name != s.Fund.Name {
				return nil, fmt.Errorf("Prabhu: unexpected scheme %q", name)
			}
			if frequency == "monthly" {
				if err := json.Unmarshal(row[3], &p.SourceLabel); err != nil {
					return nil, err
				}
			}
			s.History = append(s.History, p)
		}
	}
	if err := history.Normalize(s.History, time.Now()); err != nil {
		return nil, err
	}
	return []history.Series{s}, nil
}
