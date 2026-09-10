package sources

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/adityathebe/nepse-mutual-funds/internal/history"
)

// The chart dates are BS, even though their numeric shape resembles ISO Gregorian dates.
func fetchSanima(ctx context.Context, client *http.Client) ([]history.Series, error) {
	var result []history.Series
	for _, fund := range []struct {
		id, firstYear int
		symbol, name  string
	}{{3, 2079, "SAGF", "Sanima Growth Fund"}, {2, 2077, "SLCF", "Sanima Large Cap Fund"}, {5, 2083, "SAEF2", "Sanima Equity Fund-2"}} {
		s := history.Series{Fund: history.Fund{Symbol: fund.symbol, Name: fund.name, Source: "sanima", Manager: "Sanima Capital", SourceURL: "https://www.sanimacapital.com/", HistoryURL: fmt.Sprintf("https://www.sanimacapital.com/frontapi/en/fund-data?scheme_id=%d", fund.id)}}
		for _, frequency := range []string{"weekly", "monthly"} {
			var response struct {
				Date   []string
				Data   []float64
				Scheme struct {
					ID   int
					Name string
				}
			}
			if err := request(ctx, client, s.Fund.HistoryURL+"&type="+frequency, nil, &response); err != nil {
				return nil, err
			}
			if response.Scheme.ID != fund.id || response.Scheme.Name != s.Fund.Name {
				return nil, fmt.Errorf("Sanima: wrong scheme")
			}
			points, err := bsChartPoints(s.Fund.Symbol, frequency, response.Date, response.Data, fund.firstYear)
			if err != nil {
				return nil, err
			}
			s.History = append(s.History, points...)
		}
		if err := history.Normalize(s.History, time.Now()); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, nil
}
