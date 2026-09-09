package sources

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/adityathebe/nepse-mutual-funds/internal/history"
)

// fetchSiddhartha leaves date filters empty to collect beyond the UI's default month.
func fetchSiddhartha(ctx context.Context, client *http.Client) ([]history.Series, error) {
	var result []history.Series
	for _, fund := range []struct{ id, symbol, name string }{{"2", "SIGS2", "Siddhartha Investment Growth Scheme 2"}, {"4", "SIGS3", "Siddhartha Investment Growth Scheme 3"}} {
		s := history.Series{Fund: history.Fund{Symbol: fund.symbol, Name: fund.name, Source: "siddhartha", Manager: "Siddhartha Capital", SourceURL: "https://www.siddharthacapital.com/scheme-reports/nav-details/", HistoryURL: "https://www.siddharthacapital.com/wp-admin/admin-ajax.php?action=scheme_data_filter&scheme_id=" + fund.id}}
		seen := map[history.Point]bool{}
		for _, frequency := range []string{"weekly", "monthly"} {
			form := url.Values{"action": {"scheme_data_filter"}, "scheme_id": {fund.id}, "type": {frequency}, "year": {""}, "month": {""}, "order": {"DESC"}}
			var response struct {
				Success bool `json:"success"`
				Rows    []struct {
					SchemeID string `json:"scheme_id"`
					NAV      string `json:"nav"`
					Date     string `json:"eng_date"`
					DateBS   string `json:"nep_date"`
				} `json:"data"`
			}
			if err := request(ctx, client, "https://www.siddharthacapital.com/wp-admin/admin-ajax.php", form, &response); err != nil {
				return nil, err
			}
			if !response.Success || len(response.Rows) == 0 {
				return nil, fmt.Errorf("Siddhartha: missing %s %s history", fund.symbol, frequency)
			}
			for _, row := range response.Rows {
				if row.SchemeID != fund.id {
					return nil, fmt.Errorf("Siddhartha: wrong scheme %s", row.SchemeID)
				}
				nav, err := strconv.ParseFloat(row.NAV, 64)
				if err != nil {
					return nil, err
				}
				p := history.Point{AsOf: row.Date, DateBS: row.DateBS, NAV: nav, Frequency: frequency}
				// The API repeats some identical observations under different row IDs.
				// Only exact duplicates collapse; conflicting values still fail validation.
				if !seen[p] {
					s.History = append(s.History, p)
					seen[p] = true
				}
			}
		}
		if err := history.Normalize(s.History, time.Now()); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, nil
}
