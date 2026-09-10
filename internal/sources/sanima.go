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
	s := history.Series{Fund: history.Fund{Symbol: "SAGF", Name: "Sanima Growth Fund", Source: "sanima", Manager: "Sanima Capital", SourceURL: "https://www.sanimacapital.com/", HistoryURL: "https://www.sanimacapital.com/frontapi/en/fund-data?scheme_id=3"}}
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
		if response.Scheme.ID != 3 || response.Scheme.Name != s.Fund.Name {
			return nil, fmt.Errorf("Sanima: wrong scheme")
		}
		points, err := bsChartPoints(s.Fund.Symbol, frequency, response.Date, response.Data, 2079)
		if err != nil {
			return nil, err
		}
		s.History = append(s.History, points...)
	}
	if err := history.Normalize(s.History, time.Now()); err != nil {
		return nil, err
	}
	return []history.Series{s}, nil
}
