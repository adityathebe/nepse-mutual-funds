package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// MaturityDates reads closed-end fund maturity dates from ShareSansar's NAV table,
// because the fund managers' sites do not publish them in a consistent form.
// The matured tab is read too, so funds that already matured keep a date.
func MaturityDates(ctx context.Context, client *http.Client) (map[string]string, error) {
	const pageSize = 50 // the table rejects larger pages
	dates := map[string]string{}
	for _, fundType := range []string{"-1", "1"} { // -1: closed-end, 1: matured
		for start := 0; ; start += pageSize {
			// The endpoint answers an empty table unless the request looks like a DataTables XHR.
			q := url.Values{"draw": {"1"}, "columns[0][data]": {"symbol"}, "columns[1][data]": {"maturity_date"}, "start": {strconv.Itoa(start)}, "length": {strconv.Itoa(pageSize)}, "type": {fundType}}
			var response struct {
				RecordsTotal int `json:"recordsTotal"`
				Data         []struct {
					Symbol       string  `json:"symbol"`
					MaturityDate *string `json:"maturity_date"`
				} `json:"data"`
			}
			if err := shareSansarPage(ctx, client, "https://www.sharesansar.com/mutual-fund-navs?"+q.Encode(), &response); err != nil {
				return nil, err
			}
			for _, row := range response.Data {
				if row.MaturityDate == nil {
					continue
				}
				if _, err := time.Parse(time.DateOnly, *row.MaturityDate); err != nil {
					log.Printf("ShareSansar: ignoring maturity date %q for %s", *row.MaturityDate, row.Symbol)
					continue
				}
				dates[row.Symbol] = *row.MaturityDate
			}
			if len(response.Data) == 0 || start+len(response.Data) >= response.RecordsTotal {
				break
			}
		}
	}
	if len(dates) == 0 {
		return nil, errors.New("ShareSansar: no maturity dates")
	}
	return dates, nil
}

func shareSansarPage(ctx context.Context, client *http.Client, endpoint string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "nepse-mutual-funds/1.0 (+https://github.com/adityathebe/nepse-mutual-funds)")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &httpStatusError{endpoint: endpoint, status: resp.StatusCode}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(result); err != nil {
		return fmt.Errorf("%s: invalid JSON: %w", endpoint, err)
	}
	return nil
}
