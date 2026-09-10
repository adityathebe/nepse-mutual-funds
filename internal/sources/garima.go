package sources

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/adityathebe/nepse-mutual-funds/internal/history"
)

// Use the paginated tables to retain source BS dates alongside the chart's English dates.
func fetchGarima(ctx context.Context, client *http.Client) ([]history.Series, error) {
	s := history.Series{Fund: history.Fund{Symbol: "GSY", Name: "Garima Samriddhi Yojana", Source: "garima", Manager: "Garima Capital", SourceURL: "https://garimacapital.com/nav-details", HistoryURL: "https://garimacapital.com/nav/category-data/3"}}
	for _, frequency := range []string{"weekly", "monthly"} {
		total, last, count := -1, 1, 0
		for page := 1; page <= last; page++ {
			var response struct {
				Success  bool
				Category struct {
					ID    int
					Title string
				}
				Tables map[string]struct {
					Rows []struct {
						Date string  `json:"publish_at"`
						BS   string  `json:"publish_at_bs"`
						NAV  float64 `json:"value"`
					} `json:"data"`
					Pagination struct {
						Page  int `json:"current_page"`
						Last  int `json:"last_page"`
						Total int `json:"total"`
					}
				}
			}
			if err := request(ctx, client, fmt.Sprintf("%s?%s_page=%d", s.Fund.HistoryURL, frequency, page), nil, &response); err != nil {
				return nil, err
			}
			t, ok := response.Tables[frequency]
			if !response.Success || response.Category.ID != 3 || response.Category.Title != s.Fund.Name || !ok {
				return nil, fmt.Errorf("Garima: missing/wrong scheme history")
			}
			p := t.Pagination
			if total < 0 {
				total, last = p.Total, p.Last
			}
			if p.Page != page || p.Last != last || last < page || last > 1000 || p.Total != total || len(t.Rows) == 0 {
				return nil, fmt.Errorf("Garima: invalid %s pagination", frequency)
			}
			for _, row := range t.Rows {
				y, m, d, err := parseBS(row.BS)
				if err != nil {
					return nil, err
				}
				s.History = append(s.History, history.Point{AsOf: row.Date, DateBS: fmt.Sprintf("%04d-%02d-%02d", y, m, d), NAV: row.NAV, Frequency: frequency})
			}
			count += len(t.Rows)
		}
		if count != total {
			return nil, fmt.Errorf("Garima: incomplete %s history", frequency)
		}
	}
	if err := history.Normalize(s.History, time.Now()); err != nil {
		return nil, err
	}
	return []history.Series{s}, nil
}
