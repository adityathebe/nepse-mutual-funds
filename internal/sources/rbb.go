package sources

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/adityathebe/nepse-mutual-funds/internal/history"
)

// RBB's chart truncates history. Read every NAV-table page and use date, not publication time.
func fetchRBB(ctx context.Context, client *http.Client) ([]history.Series, error) {
	var result []history.Series
	for _, fund := range []struct{ symbol, name, slug string }{{"RBBF40", "RBB Focus 40", "rbb-focus-40"}, {"RMF2", "RBB Mutual Fund 2", "rbb-mutual-fund-2"}} {
		series, err := fetchRBBScheme(ctx, client, history.Fund{Symbol: fund.symbol, Name: fund.name, Source: "rbb", Manager: "RBB Merchant Banking", SourceURL: "https://www.rbbmbl.com.np/mutual-fund/" + fund.slug, HistoryURL: "https://api.rbbmbl.com.np/api/getSchemeNavs?fund=" + fund.slug})
		if err != nil {
			return nil, err
		}
		result = append(result, series...)
	}
	return result, nil
}

func fetchRBBScheme(ctx context.Context, client *http.Client, fund history.Fund) ([]history.Series, error) {
	s := history.Series{Fund: fund}
	for _, frequency := range []string{"weekly", "monthly"} {
		count, total := 0, -1
		for page := 1; ; page++ {
			var response struct {
				Data struct {
					Page  int `json:"current_page"`
					Last  int `json:"last_page"`
					Total int `json:"total"`
					Rows  []struct {
						NAV, Date string
						Month     *string
					} `json:"data"`
				} `json:"data"`
			}
			endpoint := fmt.Sprintf("%s&type=%s&pagination_count=100&page=%d", s.Fund.HistoryURL, frequency, page)
			if err := request(ctx, client, endpoint, nil, &response); err != nil {
				return nil, err
			}
			d := response.Data
			if total < 0 {
				total = d.Total
			}
			if d.Page != page || d.Last < page || d.Last > 1000 || d.Total != total || len(d.Rows) == 0 {
				return nil, fmt.Errorf("RBB: invalid %s pagination", frequency)
			}
			for _, row := range d.Rows {
				date, err := time.Parse("2006-01-02 15:04:05", row.Date)
				if err != nil {
					return nil, err
				}
				nav, err := strconv.ParseFloat(row.NAV, 64)
				if err != nil {
					return nil, err
				}
				p := history.Point{AsOf: date.Format(time.DateOnly), NAV: nav, Frequency: frequency}
				if row.Month != nil {
					p.SourceLabel = *row.Month
				}
				s.History = append(s.History, p)
			}
			count += len(d.Rows)
			if page == d.Last {
				if count != total {
					return nil, fmt.Errorf("RBB: got %d of %d rows", count, total)
				}
				break
			}
		}
	}
	if err := history.Normalize(s.History, time.Now()); err != nil {
		return nil, err
	}
	return []history.Series{s}, nil
}
