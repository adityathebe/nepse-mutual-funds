package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	nepse "github.com/voidarchive/go-nepse"
)

const rootURL = "https://www.nepalstock.com"

type output struct {
	SchemaVersion int     `json:"schema_version"`
	Sector        string  `json:"sector"`
	AsOf          string  `json:"as_of"`
	Companies     []ratio `json:"companies"`
}

type ratio struct {
	Symbol              string   `json:"symbol"`
	Name                string   `json:"name"`
	FiscalYear          string   `json:"fiscal_year"`
	Quarter             string   `json:"quarter"`
	ReportSubmittedDate string   `json:"report_submitted_date"`
	PriceAsOf           string   `json:"price_as_of"`
	MarketPrice         float64  `json:"market_price"`
	PE                  *float64 `json:"pe"`
	PB                  *float64 `json:"pb"`
	ROE                 *float64 `json:"roe"`
	NetMargin           *float64 `json:"net_margin"`
	DebtToEquity        *float64 `json:"debt_to_equity"`
	DividendYield       *float64 `json:"dividend_yield"`
	ReportURL           string   `json:"report_url"`
	PriceURL            string   `json:"price_url"`
	DividendURL         string   `json:"dividend_url"`
}

type statement struct {
	totalAssets     float64
	equity          float64
	operatingIncome []float64
}

var numberPattern = regexp.MustCompile(`\(?-?[0-9][0-9,.]*\)?%?`)

func main() {
	outputPath := flag.String("output", "exports/ratios.json", "output JSON path")
	flag.Parse()
	if err := run(*outputPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(outputPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	client, err := nepse.NewClient(nil)
	if err != nil {
		return err
	}
	defer client.Close()

	companies, err := client.Companies(ctx)
	if err != nil {
		return err
	}
	var banks []nepse.Company
	for _, company := range companies {
		if company.SectorName == "Commercial Banks" && company.Status == "A" && company.InstrumentType == "Equity" {
			banks = append(banks, company)
		}
	}
	if len(banks) == 0 {
		return errors.New("NEPSE returned no active commercial banks")
	}

	httpClient := &http.Client{Timeout: 90 * time.Second}
	result := output{SchemaVersion: 1, Sector: "Commercial Banks"}
	for _, bank := range banks {
		item, err := scrapeBank(ctx, client, httpClient, bank)
		if err != nil {
			return fmt.Errorf("%s: %w", bank.Symbol, err)
		}
		if item.PriceAsOf > result.AsOf {
			result.AsOf = item.PriceAsOf
		}
		result.Companies = append(result.Companies, item)
		fmt.Printf("%s: %s %s\n", item.Symbol, item.FiscalYear, item.Quarter)
	}
	sort.Slice(result.Companies, func(i, j int) bool { return result.Companies[i].Symbol < result.Companies[j].Symbol })

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	temporary := outputPath + ".tmp"
	if err := os.WriteFile(temporary, data, 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, outputPath)
}

func scrapeBank(ctx context.Context, client *nepse.Client, httpClient *http.Client, bank nepse.Company) (ratio, error) {
	reports, err := client.Reports(ctx, bank.ID)
	if err != nil {
		return ratio{}, err
	}
	report := latestQuarterly(reports)
	if report == nil || report.FiscalReport == nil || report.FiscalReport.FinancialYear == nil || report.FiscalReport.QuarterMaster == nil {
		return ratio{}, errors.New("latest quarterly report is missing")
	}
	documents := append([]nepse.ReportDocument(nil), report.ApplicationDocumentDetailsList...)
	if len(documents) == 0 {
		return ratio{}, errors.New("latest quarterly report has no document")
	}
	sort.SliceStable(documents, func(i, j int) bool {
		return documentScore(documents[i].FilePath) > documentScore(documents[j].FilePath)
	})
	var document nepse.ReportDocument
	var reportURL string
	var financials statement
	var documentErrors []error
	for _, candidate := range documents {
		candidateURL := rootURL + "/api/nots/security/fetchFiles?" + url.Values{"fileLocation": {candidate.FilePath}}.Encode()
		text, fetchErr := fetchReportText(ctx, httpClient, candidateURL)
		if fetchErr != nil {
			documentErrors = append(documentErrors, fetchErr)
			continue
		}
		parsed, parseErr := parseStatement(text)
		if parseErr != nil {
			documentErrors = append(documentErrors, parseErr)
			continue
		}
		document, reportURL, financials = candidate, candidateURL, parsed
		break
	}
	if reportURL == "" {
		return ratio{}, fmt.Errorf("no attached document contains financial statements: %w", errors.Join(documentErrors...))
	}

	detail, err := client.SecurityDetail(ctx, bank.ID)
	if err != nil {
		return ratio{}, err
	}
	price := detail.ClosePrice
	if price <= 0 {
		price = detail.LastTradedPrice
	}
	if price <= 0 {
		price = detail.PreviousClose
	}
	if price <= 0 {
		return ratio{}, errors.New("current market price is missing")
	}

	fiscal := report.FiscalReport
	scale, err := assetScale(financials.totalAssets, fiscal.PaidUpCapital)
	if err != nil {
		return ratio{}, err
	}
	financials.totalAssets *= scale
	if detail.ListedShares <= 0 {
		return ratio{}, errors.New("listed share count is missing")
	}

	bookValue := fiscal.NetWorthPerShare
	currentEquity := bookValue * float64(detail.ListedShares)
	if bookValue < 25 || bookValue > 1000 {
		if bookValue > 1000000 {
			currentEquity = bookValue
		} else {
			currentEquity = financials.equity * scale
		}
		bookValue = currentEquity / float64(detail.ListedShares)
	}
	if currentEquity <= 0 || financials.totalAssets <= currentEquity {
		return ratio{}, errors.New("assets and equity contain invalid values")
	}

	annualProfit := fiscal.EPSValue * float64(detail.ListedShares)
	ytdProfit := annualProfit * float64(fiscal.QuarterMaster.ID) / 4
	operatingIncome, err := pickOperatingIncome(financials.operatingIncome, scale, ytdProfit)
	if err != nil {
		return ratio{}, err
	}

	dividends, err := client.Dividends(ctx, bank.ID)
	if err != nil {
		return ratio{}, err
	}
	cashDividend := latestCashDividend(dividends)

	item := ratio{
		Symbol:              bank.Symbol,
		Name:                bank.SecurityName,
		FiscalYear:          fiscal.FinancialYear.FYNameNepali,
		Quarter:             fiscal.QuarterMaster.QuarterName,
		ReportSubmittedDate: document.SubmittedDate,
		PriceAsOf:           detail.BusinessDate,
		MarketPrice:         round(price),
		ReportURL:           reportURL,
		PriceURL:            fmt.Sprintf("%s/company/detail/%d", rootURL, bank.ID),
		DividendURL:         fmt.Sprintf("%s/api/nots/application/dividend/%d", rootURL, bank.ID),
	}
	if fiscal.EPSValue > 0 {
		item.PE = value(price / fiscal.EPSValue)
	}
	if bookValue > 0 {
		item.PB = value(price / bookValue)
		item.ROE = value(fiscal.EPSValue / bookValue * 100)
	}
	item.NetMargin = value(ytdProfit / operatingIncome * 100)
	item.DebtToEquity = value((financials.totalAssets - currentEquity) / currentEquity)
	if cashDividend >= 0 && detail.FaceValue > 0 {
		item.DividendYield = value(cashDividend * detail.FaceValue / price)
	}
	return item, nil
}

func latestQuarterly(reports []nepse.Report) *nepse.Report {
	var selected *nepse.Report
	key := ""
	for i := range reports {
		report := &reports[i]
		if !report.IsQuarterly() || report.ActiveStatus != "A" || report.ApplicationStatus != 3 || report.FiscalReport == nil || report.FiscalReport.FinancialYear == nil || report.FiscalReport.QuarterMaster == nil {
			continue
		}
		candidate := fmt.Sprintf("%s-%02d-%s", report.FiscalReport.FinancialYear.ToYear, report.FiscalReport.QuarterMaster.ID, report.ModifiedDate)
		if candidate > key {
			selected, key = report, candidate
		}
	}
	return selected
}

func documentScore(path string) int {
	path = strings.ToLower(path)
	score := 0
	for _, word := range []string{"financial", "quarter", "result", "report"} {
		if strings.Contains(path, word) {
			score++
		}
	}
	return score
}

func latestCashDividend(dividends []nepse.Dividend) float64 {
	latestKey := ""
	cash := 0.0
	for _, dividend := range dividends {
		if dividend.ActiveStatus != "A" || dividend.ApplicationStatus != 3 || dividend.CompanyNews == nil || dividend.CompanyNews.DividendsNotice == nil {
			continue
		}
		notice := dividend.CompanyNews.DividendsNotice
		key := dividend.ModifiedDate
		if notice.FinancialYear != nil {
			key = notice.FinancialYear.ToYear + "-" + key
		}
		if key > latestKey {
			latestKey, cash = key, notice.CashDividend
		}
	}
	return cash
}

func fetchReportText(ctx context.Context, client *http.Client, reportURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reportURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", rootURL+"/")
	response, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("report download returned HTTP %d", response.StatusCode)
	}
	var pdf bytes.Buffer
	if _, err := pdf.ReadFrom(http.MaxBytesReader(nil, response.Body, 32<<20)); err != nil {
		return "", err
	}

	command := exec.CommandContext(ctx, "pdftotext", "-layout", "-", "-")
	command.Stdin = bytes.NewReader(pdf.Bytes())
	extracted, err := command.Output()
	if err == nil {
		if _, parseErr := parseStatement(string(extracted)); parseErr == nil {
			return string(extracted), nil
		}
	}
	return ocrPDF(ctx, pdf.Bytes())
}

func ocrPDF(ctx context.Context, pdf []byte) (string, error) {
	dir, err := os.MkdirTemp("", "bank-report-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	pdfPath := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(pdfPath, pdf, 0o600); err != nil {
		return "", err
	}
	prefix := filepath.Join(dir, "page")
	if output, err := exec.CommandContext(ctx, "pdftoppm", "-f", "1", "-l", "10", "-r", "300", "-png", pdfPath, prefix).CombinedOutput(); err != nil {
		return "", fmt.Errorf("pdftoppm failed: %w: %s", err, output)
	}
	pages, err := filepath.Glob(prefix + "-*.png")
	if err != nil || len(pages) == 0 {
		return "", errors.New("PDF produced no images for OCR")
	}
	sort.Strings(pages)
	var text strings.Builder
	for _, page := range pages {
		command := exec.CommandContext(ctx, "tesseract", page, "stdout", "-l", "eng", "--psm", "6")
		output, err := command.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("tesseract failed: %w: %s", err, output)
		}
		text.Write(output)
		text.WriteByte('\n')
		if _, err := parseStatement(text.String()); err == nil {
			return text.String(), nil
		}
	}
	return "", errors.New("required financial statement rows were not found after OCR")
}

func parseStatement(text string) (statement, error) {
	assets, ok := findRow(text, "totalassets", "totalassots", "totalantots")
	if !ok {
		return statement{}, errors.New("total assets row not found")
	}
	equity, ok := findRow(text, "totalequity", "totalequlty", "totalquity")
	if !ok {
		equity, ok = findRowIncludingAttributable(text, "totalequity", "totalequlty", "totalquity")
	}
	operatingIncome, ok := findRow(text, "totaloperatingincome", "totaicperatingincame", "totaloperetingincome")
	if !ok {
		return statement{}, errors.New("total operating income row not found")
	}

	currentEquity := 0.0
	if len(equity) > 0 {
		currentEquity = equity[0]
		if len(equity) >= 4 {
			currentEquity = equity[2]
		} else if len(equity) == 3 {
			currentEquity = equity[1]
		}
	}
	return statement{totalAssets: assets[0], equity: currentEquity, operatingIncome: operatingIncome}, nil
}

func findRowIncludingAttributable(text string, labels ...string) ([]float64, bool) {
	return findRowMatching(text, true, labels...)
}

func findRow(text string, labels ...string) ([]float64, bool) {
	return findRowMatching(text, false, labels...)
}

func findRowMatching(text string, includeAttributable bool, labels ...string) ([]float64, bool) {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		compact := compactText(line)
		matched := ""
		for _, label := range labels {
			if strings.Contains(compact, label) {
				matched = label
				break
			}
		}
		if matched == "" || (!includeAttributable && (strings.Contains(compact, "attribut") || strings.Contains(compact, "atribut"))) {
			continue
		}
		values := numbers(afterLabel(line, matched))
		if len(values) == 0 && i+1 < len(lines) {
			values = numbers(lines[i+1])
		}
		if len(values) > 0 {
			return values, true
		}
	}
	return nil, false
}

func afterLabel(line, label string) string {
	target := strings.Index(compactText(line), label) + len(label)
	letters := 0
	for index, character := range strings.ToLower(line) {
		if character >= 'a' && character <= 'z' {
			letters++
		}
		if letters == target {
			return line[index+1:]
		}
	}
	return line
}

func compactText(value string) string {
	var result strings.Builder
	for _, character := range strings.ToLower(value) {
		if character >= 'a' && character <= 'z' {
			result.WriteRune(character)
		}
	}
	return result.String()
}

func numbers(line string) []float64 {
	matches := numberPattern.FindAllString(line, -1)
	result := make([]float64, 0, len(matches))
	for _, match := range matches {
		negative := strings.HasPrefix(match, "(")
		clean := strings.NewReplacer(",", "", "(", "", ")", "", "%", "").Replace(match)
		value, err := strconv.ParseFloat(clean, 64)
		if err != nil {
			continue
		}
		if negative {
			value = -value
		}
		result = append(result, value)
	}
	return result
}

func assetScale(totalAssets, paidUpCapital float64) (float64, error) {
	for _, scale := range []float64{1, 1000, 1000000} {
		ratio := totalAssets * scale / paidUpCapital
		if ratio >= 5 && ratio <= 100 {
			return scale, nil
		}
	}
	return 0, fmt.Errorf("cannot reconcile statement asset units with paid-up capital")
}

func pickOperatingIncome(candidates []float64, scale, profit float64) (float64, error) {
	minimum := math.Abs(profit)
	indices := []int{5, 1}
	for index := range candidates {
		indices = append(indices, index)
	}
	seen := map[int]bool{}
	for _, index := range indices {
		if index >= len(candidates) || seen[index] {
			continue
		}
		seen[index] = true
		candidate := candidates[index] * scale
		if candidate > minimum {
			return candidate, nil
		}
	}
	return 0, errors.New("cannot identify year-to-date total operating income")
}

func value(number float64) *float64 {
	rounded := round(number)
	return &rounded
}

func round(number float64) float64 {
	return math.Round(number*100) / 100
}
