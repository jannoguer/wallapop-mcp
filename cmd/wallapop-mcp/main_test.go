package main

import (
	"testing"
	"time"

	"github.com/jannoguer/wallapop-mcp/internal/wallapop"
)

func TestParseMinInterval(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"", wallapop.DefaultMinInterval, false},
		{"0", 0, false},
		{"1000", time.Second, false},
		{"-5", 0, true},
		{"fast", 0, true},
	}
	for _, tt := range tests {
		got, err := parseMinInterval(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("%q: got %v, %v", tt.in, got, err)
		}
	}
}
