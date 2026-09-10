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

// The official floorsheet pages select these Directus scheme IDs. Verify every
// row and filtered total so an ignored filter or truncated page cannot mix funds.
func fetchKumari(ctx context.Context, client *http.Client) ([]history.Series, error) {
	var result []history.Series
	for _, fund := range []struct {
		symbol, name, slug string
		id                 int
	}{{"KSY", "Kumari Sabal Yojana", "ksy", 2}, {"KDBY", "Kumari Dhanabriddhi Yojana", "kdby", 4}, {"KEF", "Kumari Equity Fund", "kef", 3}} {
		base := "https://api-web.kumaricapital.com/items/navs"
		s := history.Series{Fund: history.Fund{Symbol: fund.symbol, Name: fund.name, Source: "kumari", Manager: "Kumari Capital", SourceURL: "https://kumaricapital.com/floorsheet/" + fund.slug, HistoryURL: base + "?filter[scheme][_eq]=" + strconv.Itoa(fund.id)}}
		for _, frequency := range []string{"weekly", "monthly"} {
			total, count := -1, 0
			for page := 1; ; page++ {
				q := url.Values{"filter[scheme][_eq]": {strconv.Itoa(fund.id)}, "filter[frequency][_eq]": {frequency}, "sort": {"date_ad,id"}, "limit": {"100"}, "page": {strconv.Itoa(page)}, "meta": {"filter_count"}, "fields": {"id,scheme,frequency,date_ad,date_bs,value"}}
				var response struct {
					Meta struct {
						Total int `json:"filter_count"`
					}
					Rows []struct {
						Scheme    int
						Frequency string
						Date      string `json:"date_ad"`
						BS        string `json:"date_bs"`
						Value     string
					} `json:"data"`
				}
				if err := request(ctx, client, base+"?"+q.Encode(), nil, &response); err != nil {
					return nil, err
				}
				if total < 0 {
					total = response.Meta.Total
				}
				if total != response.Meta.Total || len(response.Rows) == 0 || len(response.Rows) > 100 || page > 1000 {
					return nil, fmt.Errorf("Kumari %s: invalid pagination", fund.symbol)
				}
				for _, row := range response.Rows {
					if row.Scheme != fund.id || row.Frequency != frequency {
						return nil, fmt.Errorf("Kumari: wrong scheme/frequency")
					}
					nav, err := strconv.ParseFloat(row.Value, 64)
					if err != nil {
						return nil, err
					}
					y, m, d, err := parseBS(row.BS)
					if err != nil {
						return nil, err
					}
					s.History = append(s.History, history.Point{AsOf: row.Date, DateBS: fmt.Sprintf("%04d-%02d-%02d", y, m, d), NAV: nav, Frequency: frequency})
				}
				count += len(response.Rows)
				if count == total {
					break
				}
				if count > total || len(response.Rows) < 100 {
					return nil, fmt.Errorf("Kumari: incomplete history")
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
