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
	s := history.Series{Fund: history.Fund{Symbol: "CSY", Name: "Citizens Santulit Yojana", Source: "citizens", Manager: "Citizens Capital", SourceURL: "https://www.citizenscapital.com.np/", HistoryURL: "https://www.citizenscapital.com.np/frontapi/en/getMutualFund?schemeId=5"}}
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
		if scheme.ID == 5 && scheme.Name == s.Fund.Name {
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("Citizens: Santulit scheme identity changed")
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
		previous := ""
		accepted := 0
		for i, label := range group.dates {
			y, m, d, err := parseBS(label)
			if err != nil {
				return nil, err
			}
			// The oldest CSY observations are in BS2082. Earlier years are source errors.
			if y < 2082 {
				log.Printf("Citizens: excluding %s label %q: predates CSY history", group.frequency, label)
				continue
			}
			date, err := bsToAD(y, m, d)
			if err != nil {
				return nil, err
			}
			if previous != "" && date < previous {
				log.Printf("Citizens: excluding %s label %q: date regresses in chronological feed", group.frequency, label)
				continue
			}
			previous = date
			s.History = append(s.History, history.Point{AsOf: date, DateBS: fmt.Sprintf("%04d-%02d-%02d", y, m, d), NAV: group.values[i], Frequency: group.frequency, SourceLabel: label})
			accepted++
		}
		if accepted == 0 {
			return nil, fmt.Errorf("Citizens: no usable %s history", group.frequency)
		}
	}
	if err := history.Normalize(s.History, time.Now()); err != nil {
		return nil, err
	}
	return []history.Series{s}, nil
}
