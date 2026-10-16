package shape

import (
	"sync"
	"testing"
)

// drive runs one second of demand through the pacer and returns how many
// requests it let through.
func drive(p *Pacer, sec int64, demand int) int {
	p.Roll(sec)
	n := 0
	for range demand {
		if p.Take() {
			n++
		}
	}
	return n
}

func TestPacerPriceRisesUnderASpike(t *testing.T) {
	p := NewPacer(100, 0.95)
	drive(p, 0, 95)
	drive(p, 1, 95)
	calm := p.Lambda()
	drive(p, 2, 400)
	p.Roll(3)
	spiked := p.Lambda()
	if spiked <= calm {
		t.Fatalf("price after a 4x second: %g, before: %g; want it higher", spiked, calm)
	}
	// One second can move the price by at most 4^0.6.
	if spiked > calm*2.3 {
		t.Fatalf("price jumped from %g to %g in one second", calm, spiked)
	}
}

func TestPacerPriceFallsWhenDemandDrops(t *testing.T) {
	p := NewPacer(100, 0.95)
	for sec := range int64(5) {
		drive(p, sec, 400)
	}
	high := p.Lambda()
	for sec := int64(5); sec < 10; sec++ {
		drive(p, sec, 10)
	}
	if p.Lambda() >= high {
		t.Fatalf("price stayed at %g after five quiet seconds (was %g)", p.Lambda(), high)
	}
}

func TestPacerAveragesOverIdleSeconds(t *testing.T) {
	// 95 requests spread over a five-second gap is a slow trickle, not a
	// second at target, so the price should fall.
	p := NewPacer(100, 0.95)
	drive(p, 0, 95)
	before := p.Lambda()
	for range 95 {
		p.Take()
	}
	p.Roll(6)
	if p.Lambda() >= before {
		t.Fatalf("price %g after a quiet gap, want below %g", p.Lambda(), before)
	}
}

func TestPacerCapHoldsUnderConcurrency(t *testing.T) {
	p := NewPacer(1000, 0.95)
	p.Roll(0)
	var mu sync.Mutex
	got := 0
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n := 0
			for range 1000 {
				if p.Take() {
					n++
				}
			}
			mu.Lock()
			got += n
			mu.Unlock()
		}()
	}
	wg.Wait()
	if got != 1000 {
		t.Fatalf("8 goroutines got %d requests through a cap of 1000", got)
	}
}

func TestPacerRollIsOncePerSecond(t *testing.T) {
	p := NewPacer(10, 1)
	if !p.Roll(5) {
		t.Fatal("first roll into a second should reprice")
	}
	if p.Roll(5) || p.Roll(4) {
		t.Fatal("rolling into the same or an older second repriced again")
	}
}
