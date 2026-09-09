package sources

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/adityathebe/nepse-mutual-funds/internal/history"
)

const nmbAPI = "https://www.nmbcl.com.np/frontapi/en/"

type nmbRow struct {
	ID        int     `json:"id"`
	SchemeID  int     `json:"scheme_id"`
	NAV       float64 `json:"nav"`
	Type      string  `json:"type"`
	DailyEN   string  `json:"daily_en"`
	DailyNP   string  `json:"daily_np"`
	WeeklyEN  string  `json:"weekly_en"`
	WeeklyNP  string  `json:"weekly_np"`
	MonthlyEN string  `json:"monthly_en"`
	MonthlyNP string  `json:"monthly_np"`
}

// fetchNMB uses the paginated table rather than the chart to preserve BS dates.
func fetchNMB(ctx context.Context, client *http.Client) ([]history.Series, error) {
	var registry struct {
		Scheme []struct {
			ID     int    `json:"id"`
			Name   string `json:"name"`
			Symbol string `json:"company_symbol"`
		} `json:"scheme"`
	}
	if err := request(ctx, client, nmbAPI+"scheme", nil, &registry); err != nil {
		return nil, err
	}
	name := ""
	for _, scheme := range registry.Scheme {
		if scheme.ID == 6 && scheme.Symbol == "NMBHF2" {
			name = scheme.Name
		}
	}
	if name == "" {
		return nil, fmt.Errorf("NMBHF2 scheme identity missing or changed")
	}
	s := history.Series{Fund: history.Fund{Symbol: "NMBHF2", Name: name, Source: "nmb", Manager: "NMB Capital", SourceURL: "https://www.nmbcl.com.np/navhistory", HistoryURL: nmbAPI + "getMutualFund?schemeId=6"}}
	for _, frequency := range []string{"daily", "weekly", "monthly"} {
		total, lastPage := -1, 1
		seen := map[int]bool{}
		for page := 1; page <= lastPage; page++ {
			var response struct {
				Table struct {
					CurrentPage int      `json:"current_page"`
					LastPage    int      `json:"last_page"`
					Total       int      `json:"total"`
					Rows        []nmbRow `json:"data"`
				} `json:"navtables"`
			}
			endpoint := fmt.Sprintf("%s&type=%s&page=%d", s.Fund.HistoryURL, frequency, page)
			if err := request(ctx, client, endpoint, nil, &response); err != nil {
				return nil, err
			}
			t := response.Table
			if t.CurrentPage != page || t.LastPage < page || t.LastPage > 1000 || t.Rows == nil || t.Total < 0 {
				return nil, fmt.Errorf("NMB: invalid pagination for %s page %d", frequency, page)
			}
			if page == 1 {
				total, lastPage = t.Total, t.LastPage
			}
			if total != t.Total || lastPage != t.LastPage {
				return nil, fmt.Errorf("NMB: history changed during pagination; rerun")
			}
			for _, r := range t.Rows {
				if r.SchemeID != 6 || r.Type != frequency || r.ID <= 0 || seen[r.ID] {
					return nil, fmt.Errorf("NMB: wrong scheme/type or repeated row %d", r.ID)
				}
				seen[r.ID] = true
				p := history.Point{NAV: r.NAV, Frequency: frequency}
				switch frequency {
				case "daily":
					p.AsOf, p.DateBS = r.DailyEN, r.DailyNP
				case "weekly":
					p.AsOf, p.DateBS = r.WeeklyEN, r.WeeklyNP
				case "monthly":
					p.AsOf, p.DateBS = r.MonthlyEN, r.MonthlyNP
				}
				s.History = append(s.History, p)
			}
		}
		if len(seen) != total || (frequency != "daily" && total == 0) {
			return nil, fmt.Errorf("NMB: incomplete %s history", frequency)
		}
	}
	if err := history.Normalize(s.History, time.Now()); err != nil {
		return nil, err
	}
	return []history.Series{s}, nil
}
