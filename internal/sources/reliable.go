package sources

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/adityathebe/nepse-mutual-funds/internal/history"
)

// This CMS ignores page/start offsets. Request the advertised total with limit,
// and reject truncated responses rather than repeatedly importing the first page.
func fetchReliable(ctx context.Context, client *http.Client) ([]history.Series, error) {
	s := history.Series{Fund: history.Fund{Symbol: "RSY", Name: "Reliable Samriddhi Yojana", Source: "reliable", Manager: "Reliable Investment and Merchant Capital", SourceURL: "https://reliablecapital.com.np/reliable-samridhi-yojana", HistoryURL: "https://api.reliablecapital.com.np/api/nav-charts"}}
	limit := 100
	for attempt := 0; attempt < 2; attempt++ {
		var response struct {
			Rows []struct{ Date, NAV string } `json:"data"`
			Meta struct {
				Pagination struct{ Page, PageCount, Total int } `json:"pagination"`
			} `json:"meta"`
		}
		endpoint := fmt.Sprintf("%s?sort=date:desc&pagination[limit]=%d", s.Fund.HistoryURL, limit)
		if err := request(ctx, client, endpoint, nil, &response); err != nil {
			return nil, err
		}
		p := response.Meta.Pagination
		if p.Total > limit && p.Total <= 10000 && attempt == 0 {
			limit = p.Total
			continue
		}
		if p.Page != 1 || p.PageCount != 1 || p.Total != len(response.Rows) || len(response.Rows) == 0 {
			return nil, fmt.Errorf("Reliable: incomplete history: got %d of %d rows", len(response.Rows), p.Total)
		}
		for _, row := range response.Rows {
			nav, err := strconv.ParseFloat(row.NAV, 64)
			if err != nil {
				return nil, err
			}
			s.History = append(s.History, history.Point{AsOf: row.Date, NAV: nav, Frequency: "weekly"})
		}
		break
	}
	if err := history.Normalize(s.History, time.Now()); err != nil {
		return nil, err
	}
	return []history.Series{s}, nil
}
