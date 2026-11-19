// Command wallapop-mcp is an MCP server over Wallapop's web API, on stdio.
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

	"github.com/jannoguer/wallapop-mcp/internal/server"
	"github.com/jannoguer/wallapop-mcp/internal/wallapop"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	interval, err := parseMinInterval(os.Getenv("WALLAPOP_MIN_INTERVAL_MS"))
	if err != nil {
		return err
	}
	cfg := wallapop.Config{
		MinInterval: interval,
		DefaultCity: os.Getenv("WALLAPOP_CITY"),
		Session:     os.Getenv("WALLAPOP_SESSION"),
	}
	if cfg.Session != "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		cfg.SessionFile = filepath.Join(dir, "wallapop-mcp", "session.json")
	}
	c, err := wallapop.New(cfg)
	if err != nil {
		return err
	}
	return server.New(c).Run(context.Background(), &mcp.StdioTransport{})
}

func parseMinInterval(v string) (time.Duration, error) {
	if v == "" {
		return wallapop.DefaultMinInterval, nil
	}
	ms, err := strconv.Atoi(v)
	if err != nil || ms < 0 {
		return 0, fmt.Errorf("WALLAPOP_MIN_INTERVAL_MS: want a non-negative integer, got %q", v)
	}
	return time.Duration(ms) * time.Millisecond, nil
}
