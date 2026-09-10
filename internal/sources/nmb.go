package sources

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/adityathebe/nepse-mutual-funds/internal/history"
)

const nmbAPI = "https://www.nmbcl.com.np/frontapi/en/"

type nmbRow struct {
	ID        int     `json:"id"`
	SchemeID  int     `json:"scheme_id"`
	NAV       float64 `json:"nav"`
	Type      string  `json:"type"`
	DailyEN   string  `json:"daily_en"`
	DailyNP   string  `json:"daily_np"`
	WeeklyEN  string  `json:"weekly_en"`
	WeeklyNP  string  `json:"weekly_np"`
	MonthlyEN string  `json:"monthly_en"`
	MonthlyNP string  `json:"monthly_np"`
}

// fetchNMB uses the paginated table rather than the chart to preserve BS dates.
func fetchNMB(ctx context.Context, client *http.Client) ([]history.Series, error) {
	var registry struct {
		Scheme []struct {
			ID     int    `json:"id"`
			Name   string `json:"name"`
			Symbol string `json:"company_symbol"`
		} `json:"scheme"`
	}
	if err := request(ctx, client, nmbAPI+"scheme", nil, &registry); err != nil {
		return nil, err
	}
	var result []history.Series
	for _, fund := range []struct {
		id     int
		symbol string
		name   string
	}{{6, "NMBHF2", "NMB Hybrid Fund L II"}, {5, "NSIF2", "NMB Sulav Investment Fund II"}, {3, "NMB50", "NMB 50"}} {
		name := ""
		for _, scheme := range registry.Scheme {
			if scheme.ID == fund.id && (scheme.Symbol == fund.symbol || (scheme.Symbol == "" && scheme.Name == fund.name)) {
				name = scheme.Name
			}
		}
		if name == "" {
			return nil, fmt.Errorf("%s scheme identity missing or changed", fund.symbol)
		}
		series, err := fetchNAVTable(ctx, client, history.Fund{Symbol: fund.symbol, Name: name, Source: "nmb", Manager: "NMB Capital", SourceURL: "https://www.nmbcl.com.np/navhistory", HistoryURL: fmt.Sprintf("%sgetMutualFund?schemeId=%d", nmbAPI, fund.id)}, fund.id, []string{"daily", "weekly", "monthly"})
		if err != nil {
			return nil, err
		}
		result = append(result, series...)
	}
	return result, nil
}

func fetchLSCapital(ctx context.Context, client *http.Client) ([]history.Series, error) {
	var registry struct {
		Scheme []struct {
			ID         int
			Name, Slug string
		}
		Years map[string]string
	}
	if err := request(ctx, client, "https://lscapital.com.np/frontapi/en/scheme", nil, &registry); err != nil {
		return nil, err
	}
	var years []string
	for year := range registry.Years {
		y, err := strconv.Atoi(year)
		if err != nil || y < 1900 || y > time.Now().Year() {
			return nil, fmt.Errorf("LS Capital: invalid year %q", year)
		}
		years = append(years, year)
	}
	if len(years) == 0 {
		return nil, fmt.Errorf("LS Capital: missing history years")
	}
	sort.Strings(years)
	var result []history.Series
	for _, wanted := range []struct {
		id           int
		symbol, slug string
	}{{7, "LVF2", "laxmi-value-fund-ii"}, {1, "SBCF", "sunrise-blue-chip-fund"}, {2, "SFMF", "sunrise-first-mutual-fund"}, {3, "SFEF", "sunrise-focused-equity-fund"}, {6, "LUK", "laxmi-unnati-kosh"}, {8, "LSH12", "ls-horizon-12"}} {
		name := ""
		for _, fund := range registry.Scheme {
			if fund.ID == wanted.id && fund.Slug == wanted.slug {
				name = fund.Name
			}
		}
		if name == "" {
			return nil, fmt.Errorf("LS Capital: %s scheme identity changed", wanted.symbol)
		}
		series, err := fetchNAVTable(ctx, client, history.Fund{Symbol: wanted.symbol, Name: name, Source: "lscapital", Manager: "LS Capital", SourceURL: "https://lscapital.com.np/", HistoryURL: fmt.Sprintf("https://lscapital.com.np/frontapi/en/getMutualFund?schemeId=%d", wanted.id)}, wanted.id, []string{"weekly", "monthly"}, years...)
		if err != nil {
			return nil, err
		}
		result = append(result, series...)
	}
	return result, nil
}

// NMB and LS Capital use the same paginated NAV table, with explicit AD and BS fields.
func fetchNAVTable(ctx context.Context, client *http.Client, fund history.Fund, id int, frequencies []string, years ...string) ([]history.Series, error) {
	s := history.Series{Fund: fund}
	observations := map[history.Point]bool{}
	if len(years) == 0 {
		years = []string{""}
	}
	for _, frequency := range frequencies {
		for _, year := range years {
			total, lastPage := -1, 1
			seen := map[int]bool{}
			for page := 1; page <= lastPage; page++ {
				var response struct {
					Table struct {
						CurrentPage int      `json:"current_page"`
						LastPage    int      `json:"last_page"`
						Total       int      `json:"total"`
						Rows        []nmbRow `json:"data"`
					} `json:"navtables"`
				}
				endpoint := fmt.Sprintf("%s&type=%s&page=%d", s.Fund.HistoryURL, frequency, page)
				if year != "" {
					endpoint += "&year=" + year
				}
				if err := request(ctx, client, endpoint, nil, &response); err != nil {
					return nil, err
				}
				t := response.Table
				if t.CurrentPage != page || t.LastPage < page || t.LastPage > 1000 || t.Rows == nil || t.Total < 0 {
					return nil, fmt.Errorf("NMB: invalid pagination for %s page %d", frequency, page)
				}
				if page == 1 {
					total, lastPage = t.Total, t.LastPage
				}
				if total != t.Total || lastPage != t.LastPage {
					return nil, fmt.Errorf("NMB: history changed during pagination; rerun")
				}
				for _, r := range t.Rows {
					if r.SchemeID != id || r.Type != frequency || r.ID <= 0 || seen[r.ID] {
						return nil, fmt.Errorf("NMB: wrong scheme/type or repeated row %d", r.ID)
					}
					seen[r.ID] = true
					p := history.Point{NAV: r.NAV, Frequency: frequency}
					switch frequency {
					case "daily":
						p.AsOf, p.DateBS = r.DailyEN, r.DailyNP
					case "weekly":
						p.AsOf, p.DateBS = r.WeeklyEN, r.WeeklyNP
					case "monthly":
						p.AsOf, p.DateBS = r.MonthlyEN, r.MonthlyNP
					}
					if p.DateBS != "" {
						y, m, d, err := parseBS(p.DateBS)
						if err != nil {
							return nil, err
						}
						p.DateBS = fmt.Sprintf("%04d-%02d-%02d", y, m, d)
					}
					if !observations[p] {
						s.History = append(s.History, p)
						observations[p] = true
					}
				}
			}
			if len(seen) != total || (year == "" && frequency != "daily" && total == 0) {
				return nil, fmt.Errorf("NMB: incomplete %s history", frequency)
			}
		}
	}
	// LS Capital has conflicting historical rows without a reliable correction marker.
	// Exclude every version of ambiguous date/frequency pairs rather than choosing one.
	if fund.Source == "lscapital" {
		counts := map[string]int{}
		for _, p := range s.History {
			counts[p.AsOf+"/"+p.Frequency]++
		}
		points := s.History[:0]
		for _, p := range s.History {
			key := p.AsOf + "/" + p.Frequency
			if counts[key] > 1 {
				log.Printf("%s: excluding conflicting observation %s", fund.Symbol, key)
				continue
			}
			points = append(points, p)
		}
		s.History = points
	}
	if err := history.Normalize(s.History, time.Now()); err != nil {
		return nil, err
	}
	return []history.Series{s}, nil
}
