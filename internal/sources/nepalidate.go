package sources

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var bsLabel = regexp.MustCompile(`(?i)^(\d{1,2})(?:st|nd|rd|th)?[ /]+([a-z]+)[ /,]+(\d{4})$`)

func parseBS(label string) (year, month, day int, err error) {
	m := bsLabel.FindStringSubmatch(strings.TrimSpace(label))
	if m == nil {
		return 0, 0, 0, fmt.Errorf("unrecognized BS date %q", label)
	}
	months := map[string]int{"baisakh": 1, "baishakh": 1, "baishak": 1, "jestha": 2, "jeth": 2, "ashad": 3, "ashadh": 3, "ashar": 3, "asar": 3, "shrawan": 4, "shravan": 4, "bhadra": 5, "bhadau": 5, "ashwin": 6, "ashoj": 6, "asoj": 6, "kartik": 7, "karthik": 7, "mangsir": 8, "mangshir": 8, "poush": 9, "paush": 9, "magh": 10, "falgun": 11, "phalgun": 11, "chaitra": 12, "chait": 12}
	day, _ = strconv.Atoi(m[1])
	month = months[strings.ToLower(m[2])]
	year, _ = strconv.Atoi(m[3])
	if month == 0 || day < 1 || day > 32 {
		return 0, 0, 0, fmt.Errorf("invalid BS date %q", label)
	}
	return year, month, day, nil
}

// Calendar facts from medic/bikram-sambat, pinned and attributed in THIRD_PARTY.md.
// Only verified years are supported; never extrapolate a BS calendar into future years.
func bsToAD(year, month, day int) (string, error) {
	months := map[int][12]int{
		2082: {31, 31, 32, 31, 31, 31, 30, 29, 30, 29, 30, 30},
		2083: {31, 31, 32, 31, 31, 31, 30, 29, 30, 29, 30, 30},
	}
	lengths, ok := months[year]
	if !ok {
		return "", fmt.Errorf("unsupported BS calendar year %d; update verified calendar data", year)
	}
	if month < 1 || month > 12 || day < 1 || day > lengths[month-1] {
		return "", fmt.Errorf("invalid BS date %04d-%02d-%02d", year, month, day)
	}
	days := day - 1
	for y := 2082; y < year; y++ {
		for _, n := range months[y] {
			days += n
		}
	}
	for m := 0; m < month-1; m++ {
		days += lengths[m]
	}
	return time.Date(2025, 4, 14, 0, 0, 0, 0, time.UTC).AddDate(0, 0, days).Format(time.DateOnly), nil
}
