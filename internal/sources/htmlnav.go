package sources

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/adityathebe/nepse-mutual-funds/internal/history"
)

var navRows = regexp.MustCompile(`(?is)<tr\b[^>]*>(.*?)</tr>`)
var navCells = regexp.MustCompile(`(?is)<td\b[^>]*>(.*?)</td>`)
var markup = regexp.MustCompile(`<[^>]*>`)

// These two official tables have fixed AD/BS/NAV columns; reject any changed row shape.
func tablePoints(fragment, layout, frequency string, columns int) ([]history.Point, error) {
	var points []history.Point
	rows := navRows.FindAllStringSubmatch(fragment, -1)
	if len(rows) == 0 {
		return nil, fmt.Errorf("NAV table has no rows")
	}
	for _, row := range rows {
		cells := navCells.FindAllStringSubmatch(row[1], -1)
		if len(cells) != columns {
			return nil, fmt.Errorf("NAV table: expected %d columns, got %d", columns, len(cells))
		}
		text := func(i int) string {
			return strings.TrimSpace(html.UnescapeString(markup.ReplaceAllString(cells[i][1], "")))
		}
		date, err := time.Parse(layout, text(0))
		if err != nil {
			return nil, err
		}
		y, m, d, err := parseBS(text(1))
		if err != nil {
			return nil, err
		}
		nav, err := strconv.ParseFloat(text(2), 64)
		if err != nil {
			return nil, err
		}
		points = append(points, history.Point{AsOf: date.Format(time.DateOnly), DateBS: fmt.Sprintf("%04d-%02d-%02d", y, m, d), NAV: nav, Frequency: frequency})
	}
	return points, nil
}

func fetchHLI(ctx context.Context, client *http.Client) ([]history.Series, error) {
	const endpoint = "https://himalayaninvest.com/himalayan-large-cap-fund-nav"
	s := history.Series{Fund: history.Fund{Symbol: "HLICF", Name: "Himalayan Large Cap Fund", Source: "himalayaninvest", Manager: "Himalayan Investment Banker", SourceURL: endpoint, HistoryURL: endpoint}}
	var page string
	if err := request(ctx, client, endpoint, nil, &page); err != nil {
		return nil, err
	}
	table := regexp.MustCompile(`(?is)<table\b[^>]*id="joomlaNavDataTable"[^>]*>.*?<tbody>(.*?)</tbody>`).FindStringSubmatch(page)
	if table == nil {
		return nil, fmt.Errorf("Himalayan: missing NAV table")
	}
	var err error
	s.History, err = tablePoints(table[1], time.DateOnly, "weekly", 4)
	if err != nil {
		return nil, err
	}
	if err := history.Normalize(s.History, time.Now()); err != nil {
		return nil, err
	}
	return []history.Series{s}, nil
}

// The WordPress nonce expires; discover it on every run and verify total_items across pages.
func fetchNIMB(ctx context.Context, client *http.Client) ([]history.Series, error) {
	var result []history.Series
	for _, fund := range []struct{ symbol, name, slug string }{{"NIBLSTF", "NIBL Stable Fund", "nav-nibl-stable-fund"}, {"NIBLGF", "NIBL Growth Fund", "nav-nibl-growth-fund"}} {
		series, err := fetchNIMBScheme(ctx, client, history.Fund{Symbol: fund.symbol, Name: fund.name, Source: "nimb", Manager: "NIMB Ace Capital", SourceURL: "https://nimbacecapital.com/services/mutual-funds/" + fund.slug + "/", HistoryURL: "https://nimbacecapital.com/wp-admin/admin-ajax.php?action=load_mutual_fund_table&mutual_fund=" + fund.slug}, fund.slug)
		if err != nil {
			return nil, err
		}
		result = append(result, series...)
	}
	return result, nil
}

func fetchNIMBScheme(ctx context.Context, client *http.Client, fund history.Fund, slug string) ([]history.Series, error) {
	s := history.Series{Fund: fund}
	var pageHTML string
	if err := request(ctx, client, s.Fund.SourceURL, nil, &pageHTML); err != nil {
		return nil, err
	}
	nonce := regexp.MustCompile(`action:\s*'load_mutual_fund_table',\s*nonce:\s*'([^']+)'`).FindStringSubmatch(pageHTML)
	if nonce == nil {
		return nil, fmt.Errorf("NIMB: missing table nonce")
	}
	paginationRow := regexp.MustCompile(`(?is)<tr><td colspan="[34]"><div class="pagination"[^>]*>.*?</div></td></tr>`)
	for _, frequency := range []string{"weekly", "monthly"} {
		columns := 3
		if frequency == "monthly" {
			columns = 4
		}
		count, total := 0, -1
		for page := 1; ; page++ {
			form := url.Values{"action": {"load_mutual_fund_table"}, "nonce": {nonce[1]}, "mutual_fund": {slug}, "type": {frequency}, "page": {strconv.Itoa(page)}, "search": {""}, "entries": {"50"}}
			var response struct {
				Success bool
				Data    struct {
					HTML    string
					Entries int
					Total   int `json:"total_items"`
				}
			}
			if err := request(ctx, client, "https://nimbacecapital.com/wp-admin/admin-ajax.php", form, &response); err != nil {
				return nil, err
			}
			if total < 0 {
				total = response.Data.Total
			}
			if !response.Success || total <= 0 || total != response.Data.Total || response.Data.Entries != 50 || page > 1000 {
				return nil, fmt.Errorf("NIMB: invalid %s pagination", frequency)
			}
			fragment := paginationRow.ReplaceAllString(response.Data.HTML, "")
			points, err := tablePoints(fragment, "02/January/2006", frequency, columns)
			if err != nil {
				return nil, err
			}
			count += len(points)
			if count > total || len(points) > 50 {
				return nil, fmt.Errorf("NIMB: excess rows")
			}
			s.History = append(s.History, points...)
			if count == total {
				break
			}
			if len(points) != 50 {
				return nil, fmt.Errorf("NIMB: truncated table")
			}
		}
	}
	if err := history.Normalize(s.History, time.Now()); err != nil {
		return nil, err
	}
	return []history.Series{s}, nil
}
