package main

import (
	"testing"
	"time"
)

func TestStopGraceAndDrainTimeout(t *testing.T) {
	cases := []struct {
		env       string
		grace     time.Duration
		wantDrain time.Duration
	}{
		{"", 10 * time.Second, 9 * time.Second},
		{"30s", 30 * time.Second, 29 * time.Second},
		{"1s", time.Second, 500 * time.Millisecond},
		{"bogus", 10 * time.Second, 9 * time.Second},
		{"-5s", 10 * time.Second, 9 * time.Second},
	}
	for _, c := range cases {
		g := stopGrace(c.env)
		if g != c.grace {
			t.Errorf("stopGrace(%q) = %v, want %v", c.env, g, c.grace)
		}
		if d := drainTimeout(g); d != c.wantDrain {
			t.Errorf("drainTimeout(%v) = %v, want %v", g, d, c.wantDrain)
		}
		if drainTimeout(g) >= g {
			t.Errorf("drain %v must stay under the grace %v", drainTimeout(g), g)
		}
	}
}
