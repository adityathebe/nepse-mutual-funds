package sources

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/adityathebe/nepse-mutual-funds/internal/history"
)

// fetchSiddhartha leaves date filters empty to collect beyond the UI's default month.
func fetchSiddhartha(ctx context.Context, client *http.Client) ([]history.Series, error) {
	return fetchSchemeData(ctx, client, "siddhartha", "Siddhartha Capital", "https://www.siddharthacapital.com", "/scheme-reports/nav-details/", []struct{ id, symbol, name string }{{"2", "SIGS2", "Siddhartha Investment Growth Scheme 2"}, {"4", "SIGS3", "Siddhartha Investment Growth Scheme 3"}, {"1", "SEF", "Siddhartha Equity Fund"}, {"5", "SEF2", "Siddhartha Equity Fund 2"}})
}

func fetchNabil(ctx context.Context, client *http.Client) ([]history.Series, error) {
	return fetchSchemeData(ctx, client, "nabil", "Nabil Investment Banking", "https://nabilinvest.com.np", "/investment-banking/mutual-funds/", []struct{ id, symbol, name string }{{"3", "NBF2", "Nabil Balanced Fund II"}, {"4", "NBF3", "Nabil Balanced Fund III"}})
}

// Nabil and Siddhartha share the WordPress scheme_data_filter contract.
// Empty year/month filters return the full history rather than the UI's current month.
func fetchSchemeData(ctx context.Context, client *http.Client, source, manager, base, page string, funds []struct{ id, symbol, name string }) ([]history.Series, error) {
	var result []history.Series
	for _, fund := range funds {
		s := history.Series{Fund: history.Fund{Symbol: fund.symbol, Name: fund.name, Source: source, Manager: manager, SourceURL: base + page, HistoryURL: base + "/wp-admin/admin-ajax.php?action=scheme_data_filter&scheme_id=" + fund.id}}
		seen := map[history.Point]bool{}
		for _, frequency := range []string{"weekly", "monthly"} {
			form := url.Values{"action": {"scheme_data_filter"}, "scheme_id": {fund.id}, "type": {frequency}, "year": {""}, "month": {""}, "order": {"DESC"}}
			var response struct {
				Success bool `json:"success"`
				Rows    []struct {
					SchemeID string `json:"scheme_id"`
					NAV      string `json:"nav"`
					Date     string `json:"eng_date"`
					DateBS   string `json:"nep_date"`
				} `json:"data"`
			}
			if err := request(ctx, client, base+"/wp-admin/admin-ajax.php", form, &response); err != nil {
				return nil, err
			}
			if !response.Success || len(response.Rows) == 0 {
				return nil, fmt.Errorf("%s: missing %s %s history", source, fund.symbol, frequency)
			}
			for _, row := range response.Rows {
				if row.SchemeID != fund.id {
					return nil, fmt.Errorf("%s: wrong scheme %s", source, row.SchemeID)
				}
				nav, err := strconv.ParseFloat(row.NAV, 64)
				if err != nil {
					return nil, err
				}
				p := history.Point{AsOf: row.Date, DateBS: row.DateBS, NAV: nav, Frequency: frequency}
				// Keep malformed BS labels verbatim; the explicit AD date still identifies the NAV.
				if row.DateBS != "" {
					y, m, d, err := parseBS(row.DateBS)
					if err != nil {
						p.DateBS, p.SourceLabel = "", row.DateBS
						log.Printf("%s: preserving malformed BS label %q at %s", fund.symbol, row.DateBS, row.Date)
					} else {
						p.DateBS = fmt.Sprintf("%04d-%02d-%02d", y, m, d)
						// A zero AD placeholder is not a date; convert the source's valid BS date.
						if row.Date == "0000-00-00" {
							p.AsOf, err = bsToAD(y, m, d)
							if err != nil {
								return nil, err
							}
							log.Printf("%s: converted BS %s because source AD is zero", fund.symbol, p.DateBS)
						}
					}
				}
				// The API repeats some identical observations under different row IDs.
				// Only exact duplicates collapse; conflicting values still fail validation.
				if !seen[p] {
					s.History = append(s.History, p)
					seen[p] = true
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
