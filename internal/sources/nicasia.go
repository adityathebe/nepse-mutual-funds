package sources

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/adityathebe/nepse-mutual-funds/internal/history"
)

// NIC Asia's chart is truncated; the server-rendered table has paginated AD/BS dates.
// Read every page, checking scheme identity and pagination before accepting observations.
func fetchNICAsia(ctx context.Context, client *http.Client) ([]history.Series, error) {
	const base = "https://www.nicasiacapital.com/nav-details"
	tablePattern := regexp.MustCompile(`(?s)<table class="tablefull">\s*<tr>\s*<th>English Date</th>\s*<th>Nepali Date</th>\s*<th>NAV</th>\s*</tr>(.*?)</table>`)
	pagination := regexp.MustCompile(`(?s)<ul class="pagination">(.*?)</ul>`)
	pageNumber := regexp.MustCompile(`page=(\d+)`)
	currentPage := regexp.MustCompile(`aria-current="page"><span class="page-link">(\d+)</span>`)
	var result []history.Series
	for _, fund := range []struct{ id, symbol, name string }{{"8", "NICGF2", "NIC ASIA Growth Fund 2"}, {"2", "NICBF", "NIC ASIA Balanced Fund"}, {"7", "NICFC", "NIC ASIA Flexi Cap Fund"}, {"6", "NICSF", "NIC ASIA Select 30 (Index Fund)"}, {"1", "NICGF", "NIC ASIA Growth Fund"}} {
		s := history.Series{Fund: history.Fund{Symbol: fund.symbol, Name: fund.name, Source: "nicasia", Manager: "NIC ASIA Capital", SourceURL: base, HistoryURL: base + "?category=" + fund.id}}
		seen := map[history.Point]bool{}
		for _, frequency := range []struct{ id, name string }{{"1", "weekly"}, {"2", "monthly"}} {
			last := 1
			for page := 1; page <= last; page++ {
				endpoint := fmt.Sprintf("%s&type=%s&page=%d", s.Fund.HistoryURL, frequency.id, page)
				var body string
				if err := request(ctx, client, endpoint, nil, &body); err != nil {
					return nil, err
				}
				if !strings.Contains(body, "<h3>Table View of "+fund.name+"</h3>") {
					return nil, fmt.Errorf("NIC Asia: wrong scheme for %s", fund.symbol)
				}
				totalPages := 1
				if nav := pagination.FindStringSubmatch(body); nav != nil {
					active := currentPage.FindStringSubmatch(nav[1])
					if active == nil || active[1] != strconv.Itoa(page) {
						return nil, fmt.Errorf("NIC Asia: wrong current page")
					}
					totalPages = page
					for _, match := range pageNumber.FindAllStringSubmatch(nav[1], -1) {
						n, err := strconv.Atoi(match[1])
						if err != nil || n > 1000 {
							return nil, fmt.Errorf("NIC Asia: invalid page number")
						}
						if n > totalPages {
							totalPages = n
						}
					}
				}
				if page == 1 {
					last = totalPages
				}
				if totalPages != last {
					return nil, fmt.Errorf("NIC Asia: pagination changed during fetch")
				}
				table := tablePattern.FindStringSubmatch(body)
				if table == nil {
					return nil, fmt.Errorf("NIC Asia: missing NAV table")
				}
				points, err := tablePoints(table[1], "Jan 2, 2006", frequency.name, 3)
				if err != nil {
					return nil, err
				}
				for _, p := range points {
					if !seen[p] {
						s.History = append(s.History, p)
						seen[p] = true
					}
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
