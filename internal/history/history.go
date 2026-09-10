// Package history owns the static API format, validation, and merge policy.
// Sources supply observations; only this package writes published files.
package history

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"time"
)

type Point struct {
	AsOf        string  `json:"as_of"`
	NAV         float64 `json:"nav"`
	DateBS      string  `json:"date_bs,omitempty"`
	Frequency   string  `json:"frequency"`
	SourceLabel string  `json:"source_label,omitempty"`
}

type Fund struct {
	Symbol              string `json:"symbol"`
	Source              string `json:"source"`
	Manager             string `json:"manager"`
	Name                string `json:"name"`
	SourceURL           string `json:"source_url"`
	HistoryURL          string `json:"history_url"`
	MonthlyHistoryURL   string `json:"monthly_history_url,omitempty"`
	MonthlyNAVBasis     string `json:"monthly_nav_basis,omitempty"`
	LatestAsOf          string `json:"latest_as_of"`
	LastSuccessfulFetch string `json:"last_successful_fetch"`
	Points              int    `json:"points"`
}

type Manifest struct {
	SchemaVersion int    `json:"schema_version"`
	Funds         []Fund `json:"funds"`
}

type Series struct {
	Fund    Fund
	History []Point
}

var symbolPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]{0,19}$`)
var bsPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

func key(p Point) string { return p.AsOf + "/" + p.Frequency }

// Normalize sorts observations and rejects ambiguous duplicates instead of choosing a NAV.
func Normalize(points []Point, fetched time.Time) error {
	if len(points) == 0 {
		return errors.New("empty history")
	}
	today := fetched.In(time.FixedZone("Nepal", 5*3600+45*60)).Format(time.DateOnly)
	sort.Slice(points, func(i, j int) bool { return key(points[i]) < key(points[j]) })
	for i, p := range points {
		d, err := time.Parse(time.DateOnly, p.AsOf)
		if err != nil || d.Year() < 1900 || p.AsOf > today {
			return fmt.Errorf("invalid as_of %q", p.AsOf)
		}
		if p.NAV <= 0 || math.IsNaN(p.NAV) || math.IsInf(p.NAV, 0) {
			return fmt.Errorf("invalid NAV at %s", key(p))
		}
		if p.Frequency != "daily" && p.Frequency != "weekly" && p.Frequency != "monthly" {
			return fmt.Errorf("invalid frequency %q", p.Frequency)
		}
		if p.DateBS != "" && !bsPattern.MatchString(p.DateBS) {
			return fmt.Errorf("invalid BS date %q", p.DateBS)
		}
		if i > 0 && key(points[i-1]) == key(p) {
			return fmt.Errorf("duplicate observation %s", key(p))
		}
	}
	return nil
}

func validURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}

func validateSeries(s Series) error {
	f := s.Fund
	if !symbolPattern.MatchString(f.Symbol) || f.Source == "" || f.Manager == "" || f.Name == "" || !validURL(f.SourceURL) || !validURL(f.HistoryURL) || (f.MonthlyHistoryURL != "" && !validURL(f.MonthlyHistoryURL)) {
		return fmt.Errorf("invalid metadata for %q", f.Symbol)
	}
	if f.MonthlyNAVBasis != "" && f.MonthlyNAVBasis != "bs_month_end_observation" {
		return fmt.Errorf("invalid monthly NAV basis for %s", f.Symbol)
	}
	fetched, err := time.Parse(time.RFC3339, f.LastSuccessfulFetch)
	if err != nil || fetched.After(time.Now().Add(5*time.Minute)) {
		return fmt.Errorf("invalid fetch timestamp for %s", f.Symbol)
	}
	ordered := append([]Point(nil), s.History...)
	if err := Normalize(ordered, fetched); err != nil {
		return fmt.Errorf("%s: %w", f.Symbol, err)
	}
	if !reflect.DeepEqual(ordered, s.History) {
		return fmt.Errorf("%s: history is not sorted", f.Symbol)
	}
	if f.Points != len(ordered) || f.LatestAsOf != ordered[len(ordered)-1].AsOf {
		return fmt.Errorf("%s: manifest does not match history", f.Symbol)
	}
	return nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("%s: trailing JSON content", path)
	}
	return nil
}

// Load validates the complete directory, including manifest references and orphan files.
func Load(dir string) ([]Series, error) {
	var m Manifest
	if err := readJSON(filepath.Join(dir, "manifest.json"), &m); err != nil {
		return nil, err
	}
	if m.SchemaVersion != 1 || len(m.Funds) == 0 {
		return nil, errors.New("invalid or empty manifest")
	}
	result := make([]Series, 0, len(m.Funds))
	previous := ""
	for _, f := range m.Funds {
		if !symbolPattern.MatchString(f.Symbol) || f.Symbol <= previous {
			return nil, errors.New("manifest symbols must be valid, unique and sorted")
		}
		previous = f.Symbol
		s := Series{Fund: f}
		if err := readJSON(filepath.Join(dir, f.Symbol+".json"), &s.History); err != nil {
			return nil, err
		}
		if err := validateSeries(s); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	if len(files) != len(result)+1 {
		return nil, errors.New("unlisted JSON files in data directory")
	}
	return result, nil
}

// Sync merges by (as_of, frequency), retaining old observations and accepting source
// corrections. All inputs are validated before writing; no-op fetches retain metadata.
func Sync(dir string, incoming []Series, fetched time.Time) (int, error) {
	if len(incoming) == 0 {
		return 0, errors.New("no source results")
	}
	existing, err := Load(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return 0, err
		}
		entries, readErr := os.ReadDir(dir)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return 0, readErr
		}
		if len(entries) != 0 {
			return 0, errors.New("refusing to initialize nonempty invalid data directory")
		}
	}
	all := map[string]Series{}
	for _, s := range existing {
		all[s.Fund.Symbol] = s
	}
	seen := map[string]bool{}
	for _, s := range incoming {
		id := s.Fund.Symbol
		if seen[id] {
			return 0, fmt.Errorf("multiple adapters supplied %s", id)
		}
		seen[id] = true
		if err := Normalize(s.History, fetched); err != nil {
			return 0, fmt.Errorf("%s: %w", id, err)
		}
		old, exists := all[id]
		if exists && (old.Fund.Source != s.Fund.Source || old.Fund.Manager != s.Fund.Manager) {
			return 0, fmt.Errorf("ownership changed for %s", id)
		}
		merged := map[string]Point{}
		for _, p := range old.History {
			merged[key(p)] = p
		}
		for _, p := range s.History {
			merged[key(p)] = p
		}
		s.History = make([]Point, 0, len(merged))
		for _, p := range merged {
			s.History = append(s.History, p)
		}
		if err := Normalize(s.History, fetched); err != nil {
			return 0, err
		}
		s.Fund.Points = len(s.History)
		s.Fund.LatestAsOf = s.History[len(s.History)-1].AsOf
		s.Fund.LastSuccessfulFetch = old.Fund.LastSuccessfulFetch
		if !reflect.DeepEqual(s, old) {
			s.Fund.LastSuccessfulFetch = fetched.UTC().Format(time.RFC3339)
		}
		if err := validateSeries(s); err != nil {
			return 0, err
		}
		all[id] = s
	}
	m := Manifest{SchemaVersion: 1}
	for _, s := range all {
		m.Funds = append(m.Funds, s.Fund)
	}
	sort.Slice(m.Funds, func(i, j int) bool { return m.Funds[i].Symbol < m.Funds[j].Symbol })
	if err := os.MkdirAll(dir, 0755); err != nil {
		return 0, err
	}
	changed := 0
	for _, f := range m.Funds {
		wrote, err := writeJSON(filepath.Join(dir, f.Symbol+".json"), all[f.Symbol].History)
		if err != nil {
			return changed, err
		}
		if wrote {
			changed++
		}
	}
	wrote, err := writeJSON(filepath.Join(dir, "manifest.json"), m)
	if wrote {
		changed++
	}
	return changed, err
}

// Export writes symbol -> [weekly NAV, weekly AD date, monthly NAV, monthly AD date].
// Load validates chronological order; daily points never replace either pair.
// Missing frequencies remain [null, null]; dates are as-of dates, not fetch times.
func Export(dir, path string) (bool, error) {
	series, err := Load(dir)
	if err != nil {
		return false, err
	}
	values := make(map[string][4]any, len(series))
	for _, s := range series {
		var pair [4]any
		for _, p := range s.History {
			switch p.Frequency {
			case "weekly":
				pair[0], pair[1] = p.NAV, p.AsOf
			case "monthly":
				pair[2], pair[3] = p.NAV, p.AsOf
			}
		}
		values[s.Fund.Symbol] = pair
	}
	b, err := json.Marshal(values)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, err
	}
	return writeBytes(path, b)
}

// writeJSON compares bytes before atomic replacement. The manifest is written last.
func writeJSON(path string, v any) (bool, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return false, err
	}
	return writeBytes(path, append(b, '\n'))
}

// All static outputs use byte comparison and atomic replacement, including compact exports.
func writeBytes(path string, b []byte) (bool, error) {
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if bytes.Equal(old, b) {
		return false, nil
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".nav-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return false, err
	}
	if err = f.Chmod(0644); err != nil {
		f.Close()
		return false, err
	}
	if err = f.Close(); err != nil {
		return false, err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return false, err
	}
	return true, nil
}
