package main

import (
	"context"
	"testing"
	"time"
)

func TestLargeTargetPlanningWithoutExpansion(t *testing.T) {
	c := defaults()
	c.Targets = "10.0.0.0/15"
	c.Concurrency = 1000000
	c.Rate = 0
	p, e := planTargets(c)
	if e != nil || p.Count != 262144 || len(p.Ranges) != 1 {
		t.Fatalf("plan%+v err%v", p, e)
	}
	c.Targets = "10.0.0.0/8"
	if _, e = planTargets(c); e == nil {
		t.Fatal("configured task limit ignored")
	}
	c.MaxTargets = 40000000
	p, e = planTargets(c)
	if e != nil || p.Count != 33554432 {
		t.Fatalf("large configurable plan%+v %v", p, e)
	}
	m, e := newManager(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.close()
	task, e := m.create("large paused plan", c)
	if e != nil || task.Total != 33554432 || task.Status != "paused" {
		t.Fatalf("%+v %v", task, e)
	}
	c.Targets = "::/0"
	if _, e = planTargets(c); e == nil {
		t.Fatal("unrepresentable IPv6 range accepted")
	}
}
func TestTargetRangesMergeMappedAndOverlap(t *testing.T) {
	c := defaults()
	c.Targets = "\ufeff192.0.2.0/30\n192.0.2.2/31\n::ffff:192.0.2.0/126\n::ffff:192.0.2.1\n2001:db8::1"
	p, e := planTargets(c)
	if e != nil || p.Count != 10 {
		t.Fatalf("%+v %v", p, e)
	}
	seen := map[string]bool{}
	p.each(func(target Target) bool {
		k := key(target.IP, target.Port)
		if seen[k] {
			t.Error("duplicate target")
		}
		seen[k] = true
		return true
	})
	if len(seen) != 10 {
		t.Fatal(len(seen))
	}
}
func TestNoFixedConcurrencyOrRateCeiling(t *testing.T) {
	for _, rate := range []int{0, 1000000000000} {
		c := defaults()
		c.Mode = "vless"
		c.NodeTemplate = fixtureTemplate("ws")
		c.Targets = "192.0.2.1,192.0.2.2"
		c.Ports = "443"
		c.Rounds = 1
		c.Concurrency = 1000000000
		c.Rate = rate
		targets, e := prepare(c)
		if e != nil {
			t.Fatal(e)
		}
		count := 0
		scanWithNodeChecks(context.Background(), c, targets, func(r Result) { count++ }, func(context.Context, Config, Target, *nodeTemplate) NodeObservation {
			return NodeObservation{Status: 200, TTFB: 1}
		}, func(context.Context, Config, Target, *nodeTemplate) SpeedResult {
			t.Error("unexpected download")
			return SpeedResult{}
		})
		if count != 2 {
			t.Fatal(count)
		}
	}
}
func TestPacerCancelsAndHandlesUnlimited(t *testing.T) {
	for _, rate := range []int{0, 1000000000000} {
		p := newRequestPacer(rate)
		if !p.wait(context.Background()) {
			t.Fatal("unlimited/high rate failed")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if p.wait(ctx) {
			t.Fatal("cancelled request allowed")
		}
	}
	p := newRequestPacer(1)
	if !p.wait(context.Background()) {
		t.Fatal("first paced request failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	start := time.Now()
	if p.wait(ctx) || time.Since(start) > 200*time.Millisecond {
		t.Fatal("paced wait did not cancel")
	}
}
