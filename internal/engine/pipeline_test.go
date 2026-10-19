package engine

import "testing"

func TestPipelineThreadsSplitsTheBudget(t *testing.T) {
	cases := []struct{ budget, vit, dec int }{
		{4, 2, 2}, {10, 7, 3}, {20, 14, 6},
	}
	for _, c := range cases {
		vit, dec := pipelineThreads(c.budget)
		if vit != c.vit || dec != c.dec {
			t.Errorf("pipelineThreads(%d) = %d/%d, want %d/%d", c.budget, vit, dec, c.vit, c.dec)
		}
		if vit+dec > c.budget {
			t.Errorf("pipelineThreads(%d) oversubscribes: %d+%d", c.budget, vit, dec)
		}
	}
}
