package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/adityathebe/nepse-mutual-funds/internal/history"
)

// Convert the title's BS as-of date. created_at is an upload time, and the site's
// monthly/yearly views average chart values rather than publish new observations.
func fetchNepalLife(ctx context.Context, client *http.Client) ([]history.Series, error) {
	const endpoint = "https://nepallifecapital.com.np/samriddhi-yojana-investments"
	s := history.Series{Fund: history.Fund{Symbol: "NSY", Name: "Nepal Life Samriddhi Lagani Yojana", Source: "nepallife", Manager: "Nepal Life Capital", SourceURL: endpoint, HistoryURL: endpoint}}
	var page string
	if err := request(ctx, client, endpoint, nil, &page); err != nil {
		return nil, err
	}
	data := regexp.MustCompile(`(?s)const allData\s*=\s*(\[.*?\]);`).FindStringSubmatch(page)
	if data == nil {
		return nil, fmt.Errorf("Nepal Life: missing NAV data")
	}
	var rows []struct{ Title, Value string }
	if err := json.Unmarshal([]byte(data[1]), &rows); err != nil {
		return nil, err
	}
	labelPattern := regexp.MustCompile(`^Nepal Life Samriddhi Lagani Yojana\s*,\s*([A-Za-z]+)\s+(\d{1,2}),\s*(\d{4})$`)
	for _, row := range rows {
		label := labelPattern.FindStringSubmatch(row.Title)
		if label == nil {
			return nil, fmt.Errorf("Nepal Life: unrecognized NAV title %q", row.Title)
		}
		y, m, d, err := parseBS(label[2] + " " + label[1] + " " + label[3])
		if err != nil {
			return nil, err
		}
		date, err := bsToAD(y, m, d)
		if err != nil {
			return nil, err
		}
		nav, err := strconv.ParseFloat(row.Value, 64)
		if err != nil {
			return nil, err
		}
		s.History = append(s.History, history.Point{AsOf: date, DateBS: fmt.Sprintf("%04d-%02d-%02d", y, m, d), NAV: nav, Frequency: "weekly", SourceLabel: row.Title})
	}
	if err := history.Normalize(s.History, time.Now()); err != nil {
		return nil, err
	}
	return []history.Series{s}, nil
}
