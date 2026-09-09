// navcollector runs independent official-site adapters and validates the static API.
// A failing source leaves its prior snapshot intact without blocking healthy sources.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/adityathebe/nepse-mutual-funds/internal/history"
	"github.com/adityathebe/nepse-mutual-funds/internal/sources"
)

var errSourceFailure = errors.New("one or more sources failed")

func main() {
	log.SetFlags(0)
	if err := run(); err != nil {
		log.Print(err)
		// CI may publish healthy snapshots only for source errors, never write errors.
		if errors.Is(err, errSourceFailure) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return errors.New("usage: navcollector <sync|validate> [-data data] [-source all]")
	}
	command := os.Args[1]
	if command != "sync" && command != "validate" {
		return fmt.Errorf("unknown command %q", command)
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	dir := flags.String("data", "data", "static data directory")
	source := flags.String("source", "all", "source: all, nmb, prabhu, siddhartha, globalime, machhapuchchhre, rbb, nimb, himalayaninvest, reliable, citizens, nepallife (sync only)")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if command == "validate" {
		data, err := history.Load(*dir)
		if err != nil {
			return err
		}
		points := 0
		for _, s := range data {
			points += len(s.History)
		}
		log.Printf("valid: %d funds, %d observations", len(data), points)
		return nil
	}
	adapters := sources.All()
	if *source != "all" {
		selected := adapters[:0]
		for _, a := range adapters {
			if a.Name == *source {
				selected = append(selected, a)
			}
		}
		adapters = selected
		if len(adapters) == 0 {
			return fmt.Errorf("unknown source %q", *source)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client := &http.Client{Timeout: 45 * time.Second}
	var failures []error
	for _, a := range adapters {
		log.Printf("fetching %s", a.Name)
		fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		series, err := a.Fetch(fetchCtx, client)
		cancel()
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", a.Name, err))
			log.Printf("FAILED %s: %v", a.Name, err)
			continue
		}
		changed, err := history.Sync(*dir, series, time.Now())
		if err != nil {
			return fmt.Errorf("saving %s: %w", a.Name, err)
		}
		log.Printf("%s: successful fetch at %s; %d files changed", a.Name, time.Now().UTC().Format(time.RFC3339), changed)
	}
	if len(failures) > 0 {
		return fmt.Errorf("%w: %w", errSourceFailure, errors.Join(failures...))
	}
	return nil
}
