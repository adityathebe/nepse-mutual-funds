package sources

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/adityathebe/nepse-mutual-funds/internal/history"
)

// Fetch both NAV tables without the UI's year/search filters; upload timestamps are unused.
func fetchMuktinath(ctx context.Context, client *http.Client) ([]history.Series, error) {
	s := history.Series{Fund: history.Fund{Symbol: "MNMF1", Name: "Muktinath Mutual Fund 1", Source: "muktinath", Manager: "Muktinath Capital", SourceURL: "https://muktinathcapital.com/services/mutual-fund/muktinath-mutual-fund-1", HistoryURL: "https://muktinathcapital.com/frontapi/en/mutualfund?slug=muktinath-mutual-fund-1"}}
	for _, frequency := range []string{"weekly", "monthly"} {
		total, last, count := -1, 1, 0
		for page := 1; page <= last; page++ {
			var response struct {
				Fund struct {
					ID    int
					Slug  string
					Table struct {
						Page  int `json:"current_page"`
						Last  int `json:"last_page"`
						Total int
						Rows  []struct {
							Scheme          int `json:"scheme_id"`
							Type            string
							NAV             float64
							Weekly, Monthly string
							WeeklyBS        string `json:"weekly_date_bs"`
							MonthlyBS       string `json:"monthly_date_bs"`
						} `json:"data"`
					} `json:"mutualFundData"`
				} `json:"mutual_fund"`
			}
			endpoint := fmt.Sprintf("%s&type=%s&page=%d&per_page=100&search=", s.Fund.HistoryURL, frequency, page)
			if err := request(ctx, client, endpoint, nil, &response); err != nil {
				return nil, err
			}
			t := response.Fund.Table
			if total < 0 {
				total, last = t.Total, t.Last
			}
			if response.Fund.ID != 3 || response.Fund.Slug != "muktinath-mutual-fund-1" || t.Page != page || t.Last != last || last < page || last > 1000 || t.Total != total || len(t.Rows) == 0 {
				return nil, fmt.Errorf("Muktinath: wrong scheme or invalid pagination")
			}
			for _, row := range t.Rows {
				if row.Scheme != 3 || row.Type != frequency {
					return nil, fmt.Errorf("Muktinath: wrong row scheme/type")
				}
				p := history.Point{NAV: row.NAV, Frequency: frequency, AsOf: row.Weekly, DateBS: row.WeeklyBS}
				if frequency == "monthly" {
					p.AsOf, p.DateBS = row.Monthly, row.MonthlyBS
				}
				s.History = append(s.History, p)
			}
			count += len(t.Rows)
		}
		if count != total {
			return nil, fmt.Errorf("Muktinath: incomplete %s history", frequency)
		}
	}
	if err := history.Normalize(s.History, time.Now()); err != nil {
		return nil, err
	}
	return []history.Series{s}, nil
}
