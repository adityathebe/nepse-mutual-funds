package sources

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/adityathebe/nepse-mutual-funds/internal/history"
)

// LS's NAV table omits newer monthly disclosures. Supplement it with the latest
// scheme report, taking the current-month per-unit NAV, not the prior-month column.
// Report titles identify BS month ends; upload timestamps never identify NAV dates.
func supplementLSReports(ctx context.Context, client *http.Client, series []history.Series) error {
	type report struct {
		Name  string
		Files []struct{ Location string }
	}
	type selected struct {
		report report
		point  history.Point
	}
	latest := map[string]selected{}
	period := regexp.MustCompile(`(?i)\b([a-z]+)\s+(20\d{2})\b`)
	last, total, count := 1, -1, 0
	for page := 1; page <= last; page++ {
		var response struct {
			Error bool
			Data  struct {
				Reports struct {
					Current int `json:"current_page"`
					Last    int `json:"last_page"`
					Total   int
					Data    []report
				} `json:"report_content"`
			}
		}
		endpoint := fmt.Sprintf("https://lscapital.com.np/frontapi/en/reportContent?categoryid=1&page=%d", page)
		if err := request(ctx, client, endpoint, nil, &response); err != nil {
			return err
		}
		r := response.Data.Reports
		if page == 1 {
			last, total = r.Last, r.Total
		}
		if response.Error || r.Current != page || r.Last != last || r.Total != total || last < 1 || last > 1000 || len(r.Data) == 0 {
			return fmt.Errorf("LS reports: invalid pagination")
		}
		count += len(r.Data)
		for _, item := range r.Data {
			title := strings.Join(strings.Fields(strings.ToUpper(item.Name)), " ")
			title = strings.ReplaceAll(title, "LVF 2", "LVF2")
			title = strings.ReplaceAll(title, "_", " ")
			if !strings.Contains(title, "MONTHLY NAV") {
				continue
			}
			for _, s := range series {
				if !strings.Contains(" "+title+" ", " "+s.Fund.Symbol+" ") {
					continue
				}
				matches := period.FindAllStringSubmatch(title, -1)
				if len(matches) != 1 {
					return fmt.Errorf("LS reports: unknown period in %q", item.Name)
				}
				match := matches[0]
				y, m, _, err := parseBS("1 " + match[1] + " " + match[2])
				if err != nil {
					return err
				}
				if _, err := bsToAD(y, m, 1); err != nil {
					return err
				}
				var date string
				day := 32
				for ; day >= 29; day-- {
					date, err = bsToAD(y, m, day)
					if err == nil {
						break
					}
				}
				if err != nil {
					return err
				}
				old, ok := latest[s.Fund.Symbol]
				if ok && old.point.AsOf == date {
					return fmt.Errorf("LS reports: ambiguous reports for %s at %s", s.Fund.Symbol, date)
				}
				if !ok || date > old.point.AsOf {
					latest[s.Fund.Symbol] = selected{item, history.Point{AsOf: date, DateBS: fmt.Sprintf("%04d-%02d-%02d", y, m, day), Frequency: "monthly", SourceLabel: item.Name}}
				}
			}
		}
	}
	if count != total {
		return fmt.Errorf("LS reports: incomplete catalog")
	}
	// These reports use the legacy Preeti font. Its per-unit NAV label extracts as
	// this ASCII text; require exactly one row and take its first numeric column.
	navRow := regexp.MustCompile(`(?m)^\s*k\|lt OsfO\{ v'b (?:;DklQ )?d["']No\s+([0-9]+\.[0-9]{2})\s+([0-9]+\.[0-9]{2})(?:\s|$)`)
	for i := range series {
		s := &series[i]
		item, ok := latest[s.Fund.Symbol]
		if !ok || len(item.report.Files) != 1 {
			return fmt.Errorf("LS reports: missing unique PDF for %s", s.Fund.Symbol)
		}
		pdfURL := item.report.Files[0].Location
		if !strings.HasPrefix(pdfURL, "https://lscapital.com.np/site_uploads/") || !strings.HasSuffix(pdfURL, ".pdf") {
			return fmt.Errorf("LS reports: unexpected PDF URL")
		}
		var pdf string
		if err := request(ctx, client, pdfURL, nil, &pdf); err != nil {
			return err
		}
		convertCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		cmd := exec.CommandContext(convertCtx, "pdftotext", "-layout", "-", "-")
		cmd.Stdin = strings.NewReader(pdf)
		text, err := cmd.Output()
		cancel()
		if err != nil {
			return fmt.Errorf("LS reports: pdftotext (poppler-utils required): %w", err)
		}
		matches := navRow.FindAllStringSubmatch(string(text), -1)
		if len(matches) != 1 {
			return fmt.Errorf("LS reports: expected one per-unit NAV row in %s", pdfURL)
		}
		item.point.NAV, err = strconv.ParseFloat(matches[0][1], 64)
		if err != nil {
			return err
		}
		found := false
		for _, p := range s.History {
			if p.Frequency == "monthly" && p.AsOf == item.point.AsOf {
				if p.NAV != item.point.NAV {
					return fmt.Errorf("LS reports: table/PDF NAV conflict for %s", s.Fund.Symbol)
				}
				found = true
			}
		}
		if !found {
			s.History = append(s.History, item.point)
		}
		s.Fund.MonthlyHistoryURL = pdfURL
		if err := history.Normalize(s.History, time.Now()); err != nil {
			return err
		}
	}
	return nil
}
