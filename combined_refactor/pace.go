package main

import (
	"context"
	"sync"
	"time"
)

// A zero rate disables pacing. Large rates naturally reach timer granularity;
// no ticker with a zero duration is created.
type requestPacer struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

func newRequestPacer(rate int) *requestPacer {
	p := &requestPacer{}
	if rate > 0 {
		p.interval = time.Second / time.Duration(rate)
	}
	return p
}
func (p *requestPacer) wait(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	if p.interval == 0 {
		return true
	}
	p.mu.Lock()
	now := time.Now()
	if p.next.Before(now) {
		p.next = now
	}
	delay := p.next.Sub(now)
	p.next = p.next.Add(p.interval)
	p.mu.Unlock()
	if delay <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}
