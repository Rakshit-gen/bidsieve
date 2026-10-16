package shape

import "sync"

// DupeKey identifies an impression that may reach the exchange more than
// once, through different wrappers or resellers.
//
// Fingerprint should hash the fields that are the same on every copy:
// publisher, page or app bundle, slot, and a device or user ID. Use
// Fingerprint() to build it. TID is the OpenRTB source.tid, but only set it
// when you trust it to be shared across paths. Since Prebid 8 most wrappers
// send a different tid to every bidder, so a tid match proves a duplicate
// while a tid mismatch proves nothing.
type DupeKey struct {
	Fingerprint uint64
	TID         uint64
}

// Fingerprint hashes the parts of a request that every copy of the same
// impression shares.
func Fingerprint(publisher, page, slot, device string) uint64 {
	h := HashString(publisher)
	h = Mix2(h, HashString(page))
	h = Mix2(h, HashString(slot))
	return Mix2(h, HashString(device))
}

type dupEntry struct {
	first int64
	mask  uint64 // buyers this impression already went to
}

// dedupe remembers, for a short window, which buyers each impression was
// sent to. Two generations of maps per shard keep memory bounded without a
// background goroutine: entries older than a generation fall away when the
// map holding them is replaced.
type dedupe struct {
	window    int64 // fingerprint match window, nanos
	tidWindow int64
	fp, tid   *dupTable
}

type dupShard struct {
	mu        sync.Mutex
	epoch     int64
	cur, prev map[uint64]dupEntry
}

type dupTable struct {
	gen    int64
	shards [numShards]dupShard
}

func newDupTable(gen int64) *dupTable {
	t := &dupTable{gen: gen}
	for i := range t.shards {
		t.shards[i].cur = map[uint64]dupEntry{}
		t.shards[i].prev = map[uint64]dupEntry{}
	}
	return t
}

func (t *dupTable) lookup(key uint64, now, window int64) uint64 {
	sh := &t.shards[shardOf(key)]
	sh.mu.Lock()
	t.rotate(sh, now)
	e, ok := sh.cur[key]
	if !ok {
		e, ok = sh.prev[key]
	}
	sh.mu.Unlock()
	if ok && now-e.first <= window {
		return e.mask
	}
	return 0
}

func (t *dupTable) record(key uint64, now, window int64, mask uint64) {
	sh := &t.shards[shardOf(key)]
	sh.mu.Lock()
	t.rotate(sh, now)
	e, ok := sh.cur[key]
	if !ok {
		e, ok = sh.prev[key]
	}
	if ok && now-e.first <= window {
		e.mask |= mask
	} else {
		e = dupEntry{first: now, mask: mask}
	}
	sh.cur[key] = e
	sh.mu.Unlock()
}

func (t *dupTable) rotate(sh *dupShard, now int64) {
	ep := now / t.gen
	if ep == sh.epoch {
		return
	}
	if ep == sh.epoch+1 {
		sh.prev = sh.cur
	} else {
		sh.prev = map[uint64]dupEntry{}
	}
	sh.cur = make(map[uint64]dupEntry, len(sh.prev))
	sh.epoch = ep
}

// An entry written at time t is looked up until t+window. Generations as
// long as the window guarantee it is still in cur or prev until then.
func newDedupe(window, tidWindow int64) *dedupe {
	return &dedupe{
		window:    window,
		tidWindow: tidWindow,
		fp:        newDupTable(window),
		tid:       newDupTable(tidWindow),
	}
}

const tidSalt = 0x5bd1e9955bd1e995

// A zero key means the caller has nothing to match on.
func (d *dedupe) seen(k DupeKey, now int64) uint64 {
	var m uint64
	if k.Fingerprint != 0 {
		m = d.fp.lookup(k.Fingerprint, now, d.window)
	}
	if k.TID != 0 {
		m |= d.tid.lookup(k.TID^tidSalt, now, d.tidWindow)
	}
	return m
}

func (d *dedupe) record(k DupeKey, now int64, mask uint64) {
	if k.Fingerprint != 0 {
		d.fp.record(k.Fingerprint, now, d.window, mask)
	}
	if k.TID != 0 {
		d.tid.record(k.TID^tidSalt, now, d.tidWindow, mask)
	}
}
