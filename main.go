package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const defaultMinInterval = 250 * time.Millisecond

type App struct {
	wp          *Client
	geo         *Geocoder
	cats        categoryCache
	defaultCity string
}

func main() {
	interval, err := parseMinInterval(os.Getenv("WALLAPOP_MIN_INTERVAL_MS"))
	if err != nil {
		log.Fatal(err)
	}
	a := &App{wp: NewClient(interval), geo: NewGeocoder(), defaultCity: os.Getenv("WALLAPOP_CITY")}
	if seed := os.Getenv("WALLAPOP_SESSION"); seed != "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			log.Fatal(err)
		}
		if a.wp.sess, err = newSession(seed, filepath.Join(dir, "wallapop-mcp", "session.json")); err != nil {
			log.Fatal(err)
		}
	}
	if err := newServer(a).Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}

func parseMinInterval(v string) (time.Duration, error) {
	if v == "" {
		return defaultMinInterval, nil
	}
	ms, err := strconv.Atoi(v)
	if err != nil || ms < 0 {
		return 0, fmt.Errorf("WALLAPOP_MIN_INTERVAL_MS: want a non-negative integer, got %q", v)
	}
	return time.Duration(ms) * time.Millisecond, nil
}
