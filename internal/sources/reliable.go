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
	var result []history.Series
	for _, fund := range []struct{ symbol, name, slug, endpoint string }{{"RSY", "Reliable Samriddhi Yojana", "reliable-samridhi-yojana", "nav-charts"}, {"RSY2", "Reliable Samriddhi Yojana 2", "reliable-samridhi-yojana-two", "nav-chart-2"}} {
		s := history.Series{Fund: history.Fund{Symbol: fund.symbol, Name: fund.name, Source: "reliable", Manager: "Reliable Investment and Merchant Capital", SourceURL: "https://reliablecapital.com.np/" + fund.slug, HistoryURL: "https://api.reliablecapital.com.np/api/" + fund.endpoint}}
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
		var err error
		s.History, err = withMonthEndNAVs(s.History)
		if err != nil {
			return nil, err
		}
		s.Fund.MonthlyNAVBasis = "bs_month_end_observation"
		if err := history.Normalize(s.History, time.Now()); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, nil
}
