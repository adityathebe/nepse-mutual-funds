package sources

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/adityathebe/nepse-mutual-funds/internal/history"
)

// Citizens exposes BS labels, not Gregorian NAV dates. Invalid dates are never repaired.
func fetchCitizens(ctx context.Context, client *http.Client) ([]history.Series, error) {
	var result []history.Series
	for _, fund := range []struct {
		symbol, name  string
		id, firstYear int
	}{{"CSY", "Citizens Santulit Yojana", 5, 2082}, {"C30MF", "Citizens Super 30 Mutual Fund", 3, 2080}} {
		series, err := fetchCitizensScheme(ctx, client, history.Fund{Symbol: fund.symbol, Name: fund.name, Source: "citizens", Manager: "Citizens Capital", SourceURL: "https://www.citizenscapital.com.np/", HistoryURL: fmt.Sprintf("https://www.citizenscapital.com.np/frontapi/en/getMutualFund?schemeId=%d", fund.id)}, fund.id, fund.firstYear)
		if err != nil {
			return nil, err
		}
		result = append(result, series...)
	}
	return result, nil
}

func fetchCitizensScheme(ctx context.Context, client *http.Client, fund history.Fund, id, firstYear int) ([]history.Series, error) {
	s := history.Series{Fund: fund}
	var schemes struct {
		Data struct {
			Schemes []struct {
				ID   int
				Name string
			}
		}
	}
	if err := request(ctx, client, "https://www.citizenscapital.com.np/frontapi/en/schemes", nil, &schemes); err != nil {
		return nil, err
	}
	found := false
	for _, scheme := range schemes.Data.Schemes {
		if scheme.ID == id && scheme.Name == s.Fund.Name {
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("Citizens: %s scheme identity changed", fund.Symbol)
	}
	var response struct {
		WeeklyDate, MonthlyDate []string
		WeeklyData, MonthlyData []float64
	}
	if err := request(ctx, client, s.Fund.HistoryURL, nil, &response); err != nil {
		return nil, err
	}
	for _, group := range []struct {
		frequency string
		dates     []string
		values    []float64
	}{{"weekly", response.WeeklyDate, response.WeeklyData}, {"monthly", response.MonthlyDate, response.MonthlyData}} {
		if len(group.dates) == 0 || len(group.dates) != len(group.values) {
			return nil, fmt.Errorf("Citizens: missing/misaligned %s history", group.frequency)
		}
		points, err := bsChartPoints(fund.Symbol, group.frequency, group.dates, group.values, firstYear)
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

// These chronological feeds contain missing days and mistyped years. Omit those
// rows with diagnostics; never infer a date from neighboring observations.
func bsChartPoints(symbol, frequency string, labels []string, values []float64, firstYear int) ([]history.Point, error) {
	if len(labels) == 0 || len(labels) != len(values) {
		return nil, fmt.Errorf("%s: missing/misaligned %s history", symbol, frequency)
	}
	var points []history.Point
	previous := ""
	for i, label := range labels {
		y, m, d, err := parseBS(label)
		if err != nil || y < firstYear {
			log.Printf("%s: excluding %s label %q: missing/invalid BS date", symbol, frequency, label)
			continue
		}
		date, err := bsToAD(y, m, d)
		if err != nil {
			return nil, err
		}
		if previous != "" && date < previous {
			log.Printf("%s: excluding %s label %q: date regresses in chronological feed", symbol, frequency, label)
			continue
		}
		previous = date
		points = append(points, history.Point{AsOf: date, DateBS: fmt.Sprintf("%04d-%02d-%02d", y, m, d), NAV: values[i], Frequency: frequency, SourceLabel: label})
	}
	if err := history.Normalize(points, time.Now()); err != nil {
		return nil, err
	}
	return points, nil
}
