package shape

import (
	"testing"
	"time"
)

const ms = int64(time.Millisecond)

func TestDedupeFingerprintWindow(t *testing.T) {
	d := newDedupe(300*ms, 10_000*ms)
	k := DupeKey{Fingerprint: 11}
	d.record(k, 1000*ms, 0b101)
	if m := d.seen(k, 1200*ms); m != 0b101 {
		t.Fatalf("200ms later: mask %b, want 101", m)
	}
	if m := d.seen(k, 1400*ms); m != 0 {
		t.Fatalf("400ms later, past a 300ms window: mask %b, want 0", m)
	}
}

func TestDedupeMergesBuyersWithinTheWindow(t *testing.T) {
	d := newDedupe(300*ms, 10_000*ms)
	k := DupeKey{Fingerprint: 11}
	d.record(k, 0, 0b01)
	d.record(k, 100*ms, 0b10)
	if m := d.seen(k, 200*ms); m != 0b11 {
		t.Fatalf("mask %b, want both buyers", m)
	}
	// The window runs from the first sighting, so a late copy doesn't keep
	// an impression alive forever.
	if m := d.seen(k, 350*ms); m != 0 {
		t.Fatalf("mask %b at 350ms, want the entry expired", m)
	}
}

func TestDedupeTIDOutlivesFingerprint(t *testing.T) {
	d := newDedupe(300*ms, 10_000*ms)
	k := DupeKey{Fingerprint: 11, TID: 99}
	d.record(k, 0, 0b1)
	// A copy five seconds later with a different fingerprint but the same
	// shared tid is still the same impression.
	if m := d.seen(DupeKey{Fingerprint: 12, TID: 99}, 5000*ms); m != 0b1 {
		t.Fatalf("tid match after 5s: mask %b, want 1", m)
	}
	if m := d.seen(DupeKey{Fingerprint: 11}, 5000*ms); m != 0 {
		t.Fatalf("fingerprint alone after 5s: mask %b, want 0", m)
	}
}

func TestDedupeIgnoresZeroKeys(t *testing.T) {
	d := newDedupe(300*ms, 10_000*ms)
	d.record(DupeKey{}, 0, 0b1)
	if m := d.seen(DupeKey{}, 10*ms); m != 0 {
		t.Fatalf("an empty key matched: mask %b", m)
	}
}

func TestDedupeForgetsAfterLongGaps(t *testing.T) {
	d := newDedupe(300*ms, 300*ms)
	k := DupeKey{Fingerprint: 11}
	d.record(k, 0, 0b1)
	// Jump many generations ahead; the old entry must be gone, not
	// resurrected from a stale generation.
	if m := d.seen(k, 10_000*ms); m != 0 {
		t.Fatalf("mask %b after 10s, want 0", m)
	}
	d.record(k, 10_000*ms, 0b10)
	if m := d.seen(k, 10_100*ms); m != 0b10 {
		t.Fatalf("mask %b, want only the new buyer", m)
	}
}
