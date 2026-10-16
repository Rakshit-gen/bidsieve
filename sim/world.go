// Package sim is a small ad exchange with buyers whose behaviour is known.
//
// Real bid logs don't tell you what a buyer would have done with the
// requests it never saw, which is exactly what a traffic shaper needs to be
// judged on. Here every buyer's answer to every impression is fixed in
// advance by a hash of the impression and the buyer, so any policy can be
// replayed against the same world and its misses counted exactly.
package sim

import (
	"math"
	"math/rand/v2"

	"github.com/Rakshit-gen/bidsieve/shape"
)

// Country indexes, matching shape.Countries.
const (
	usa uint8 = 1 + iota
	gbr
	deu
	fra
	ind
	bra
	jpn
	can
	aus
	esp
	ita
	mex
	numCountries = 12
)

var countryWeights = [numCountries + 1]float64{0, .30, .08, .07, .06, .12, .07, .06, .04, .03, .04, .04, .09}

// Scenario sets up a run. Times are in seconds from the start.
type Scenario struct {
	Name    string
	Seed    uint64
	Seconds int
	Rate    float64 // impressions per second, before duplicate copies

	Publishers    int
	NewPublishers int     // publishers that go live at NewPubAt
	NewPubAt      int     // 0 means never
	NewPubShare   float64 // share of traffic the new publishers carry

	// At DriftAt one buyer starts a campaign on a segment it ignored until
	// then, and another ends its biggest campaign. 0 means no drift.
	DriftAt int

	SpikeAt, SpikeLen int
	SpikeMult         float64

	Copies    bool    // impressions reach the exchange through several paths
	SharedTID float64 // chance a copy carries the impression's shared tid
	Reloads   float64 // chance a page view is reloaded within two seconds

	BuyerCaps []float64 // each buyer's cap as a share of Rate
	Bucket    int       // seconds per time-series bucket
}

// Standard is the scenario the README numbers come from.
func Standard() Scenario {
	return Scenario{
		Name:          "standard",
		Seed:          7,
		Seconds:       1200,
		Rate:          1500,
		Publishers:    400,
		NewPublishers: 60,
		NewPubAt:      720,
		NewPubShare:   0.12,
		DriftAt:       480,
		SpikeAt:       960,
		SpikeLen:      60,
		SpikeMult:     2,
		Copies:        true,
		SharedTID:     0.35,
		Reloads:       0.03,
		BuyerCaps:     []float64{0.5, 0.35, 0.3, 0.25, 0.25, 0.2, 0.15, 0.12, 0.1, 0.08},
		Bucket:        10,
	}
}

// The two buyers the drift events happen to.
const (
	driftBuyer   = 2 // starts bidding on European video
	droppedBuyer = 5 // stops bidding on US and Indian mobile banners
)

func inDriftSegment(f shape.Features) bool {
	return f.Format == shape.Video && (f.Country == deu || f.Country == fra || f.Country == gbr) &&
		(f.Device == shape.CTV || f.Device == shape.Desktop)
}

func inDroppedSegment(f shape.Features) bool {
	return (f.Format == shape.Banner300x250 || f.Format == shape.Banner320x50) &&
		(f.Country == usa || f.Country == ind) && f.Device == shape.Mobile
}

type pubKind uint8

const (
	webPub pubKind = iota
	appPub
	ctvPub
)

type slot struct {
	format shape.Format
	pos    shape.Position
}

type publisher struct {
	id      uint32
	kind    pubKind
	home    uint8
	slots   []slot
	quality float64
	paths   int // extra routes its impressions take to reach the exchange
	pages   int
	isNew   bool
}

type campaign struct {
	formats   uint16 // bit per format
	countries uint64 // bit per country, 0 for all
	devices   uint8
	channels  uint8 // bit 0 web, bit 1 app
	aboveFold bool
	pubs      map[uint32]bool // nil for all
	p, price  float64
	from      float64
	until     float64 // 0 for never
}

func (c *campaign) matches(f shape.Features, t float64) bool {
	if t < c.from || (c.until > 0 && t >= c.until) {
		return false
	}
	if c.formats&(1<<f.Format) == 0 || c.devices&(1<<f.Device) == 0 || c.channels&(1<<f.Channel) == 0 {
		return false
	}
	if c.countries != 0 && c.countries&(1<<f.Country) == 0 {
		return false
	}
	if c.aboveFold && f.Pos != shape.AboveFold {
		return false
	}
	return c.pubs == nil || c.pubs[f.Publisher]
}

type buyer struct {
	name  string
	cap   int
	base  float64 // chance of a bid on anything, from broad campaigns
	camps []campaign
}

// Typical CPMs by format. CTV video is priced on top of this.
var formatCPM = [...]float64{
	shape.BannerOther:   0.5,
	shape.Banner300x250: 1.2,
	shape.Banner728x90:  0.9,
	shape.Banner320x50:  0.6,
	shape.Banner160x600: 0.7,
	shape.Video:         6,
	shape.Native:        1.8,
}

func deviceMult(d shape.Device) float64 {
	if d == shape.CTV {
		return 1.8
	}
	return 1
}

// World is the fixed part of a scenario: publishers and buyers.
type World struct {
	sc     Scenario
	pubs   []*publisher // index = publisher id - 1
	oldCum []float64    // cumulative traffic weights, original publishers
	newCum []float64
	buyers []buyer
}

// NewWorld builds the publishers and buyers for a scenario. The same
// scenario always builds the same world.
func NewWorld(sc Scenario) *World {
	rng := rand.New(rand.NewPCG(sc.Seed, 0x1f2e3d4c))
	w := &World{sc: sc}
	for i := range sc.Publishers + sc.NewPublishers {
		w.pubs = append(w.pubs, newPublisher(rng, uint32(i+1), i >= sc.Publishers))
	}
	w.oldCum = zipfCum(rng, sc.Publishers)
	w.newCum = zipfCum(rng, sc.NewPublishers)
	for i, share := range sc.BuyerCaps {
		w.buyers = append(w.buyers, w.newBuyer(rng, i, share))
	}
	return w
}

func zipfCum(rng *rand.Rand, n int) []float64 {
	if n == 0 {
		return nil
	}
	ws := make([]float64, n)
	for i := range ws {
		ws[i] = 1 / math.Pow(float64(i+1), 1.05)
	}
	rng.Shuffle(n, func(a, b int) { ws[a], ws[b] = ws[b], ws[a] })
	cum := make([]float64, n)
	var s float64
	for i, x := range ws {
		s += x
		cum[i] = s
	}
	for i := range cum {
		cum[i] /= s
	}
	return cum
}

func pick(rng *rand.Rand, cum []float64) int {
	u := rng.Float64()
	lo, hi := 0, len(cum)-1
	for lo < hi {
		mid := (lo + hi) / 2
		if cum[mid] < u {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func weighted(rng *rand.Rand, ws []float64) int {
	var s float64
	for _, x := range ws {
		s += x
	}
	u := rng.Float64() * s
	for i, x := range ws {
		if u < x {
			return i
		}
		u -= x
	}
	return len(ws) - 1
}

func randCountry(rng *rand.Rand) uint8 { return uint8(weighted(rng, countryWeights[:])) }

func newPublisher(rng *rand.Rand, id uint32, isNew bool) *publisher {
	p := &publisher{id: id, home: randCountry(rng), isNew: isNew, pages: 20 + rng.IntN(180)}
	p.kind = pubKind(weighted(rng, []float64{.65, .25, .10}))
	p.quality = math.Exp(0.35 * rng.NormFloat64())
	p.paths = weighted(rng, []float64{.45, .30, .15, .10})
	var formats []shape.Format
	var fw []float64
	n := 1
	switch p.kind {
	case webPub:
		formats = []shape.Format{shape.Banner300x250, shape.Banner728x90, shape.Banner160x600, shape.Banner320x50, shape.Native, shape.Video}
		fw = []float64{.3, .2, .1, .15, .15, .1}
		n = 2 + rng.IntN(4)
	case appPub:
		formats = []shape.Format{shape.Banner320x50, shape.Banner300x250, shape.Native, shape.Video}
		fw = []float64{.35, .25, .25, .15}
		n = 1 + rng.IntN(3)
	case ctvPub:
		formats = []shape.Format{shape.Video}
		fw = []float64{1}
		n = 1 + rng.IntN(2)
	}
	for range n {
		f := formats[weighted(rng, fw)]
		pos := shape.PosUnknown
		switch f {
		case shape.Video:
			pos = shape.AboveFold
		case shape.Native:
		default:
			pos = shape.AboveFold
			if rng.IntN(2) == 0 {
				pos = shape.BelowFold
			}
		}
		p.slots = append(p.slots, slot{f, pos})
	}
	return p
}

func (w *World) newBuyer(rng *rand.Rand, idx int, capShare float64) buyer {
	b := buyer{
		name: "dsp" + string(rune('0'+idx/10)) + string(rune('0'+idx%10)),
		cap:  int(math.Round(capShare * w.sc.Rate)),
		base: 0.004,
	}
	n := 6 + rng.IntN(7)
	for len(b.camps) < n {
		c := w.randCampaign(rng)
		if idx == driftBuyer && w.sc.DriftAt > 0 && touchesDriftSegment(&c) {
			continue // keep the segment truly new to this buyer
		}
		b.camps = append(b.camps, c)
	}
	if w.sc.DriftAt > 0 {
		switch idx {
		case driftBuyer:
			b.camps = append(b.camps, campaign{
				formats:   1 << shape.Video,
				countries: 1<<deu | 1<<fra | 1<<gbr,
				devices:   1<<shape.CTV | 1<<shape.Desktop,
				channels:  3,
				p:         0.35,
				price:     7,
				from:      float64(w.sc.DriftAt),
			})
		case droppedBuyer:
			b.camps = append(b.camps, campaign{
				formats:   1<<shape.Banner300x250 | 1<<shape.Banner320x50,
				countries: 1<<usa | 1<<ind,
				devices:   1 << shape.Mobile,
				channels:  3,
				p:         0.25,
				price:     1.5,
				until:     float64(w.sc.DriftAt),
			})
		}
	}
	return b
}

func touchesDriftSegment(c *campaign) bool {
	eu := uint64(1<<deu | 1<<fra | 1<<gbr)
	return c.formats&(1<<shape.Video) != 0 &&
		(c.countries == 0 || c.countries&eu != 0) &&
		c.devices&(1<<shape.CTV|1<<shape.Desktop) != 0
}

func (w *World) randCampaign(rng *rand.Rand) campaign {
	c := campaign{channels: 3}
	var main shape.Format
	switch r := rng.Float64(); {
	case r < 0.25:
		main = shape.Video
		c.formats = 1 << shape.Video
		for c.devices == 0 {
			for _, d := range []shape.Device{shape.Desktop, shape.Mobile, shape.CTV} {
				if rng.IntN(2) == 0 {
					c.devices |= 1 << d
				}
			}
		}
	case r < 0.40:
		main = shape.Native
		c.formats = 1 << shape.Native
	default:
		sizes := []shape.Format{shape.Banner300x250, shape.Banner728x90, shape.Banner320x50, shape.Banner160x600}
		k := 1 + rng.IntN(3)
		for _, i := range rng.Perm(len(sizes))[:k] {
			c.formats |= 1 << sizes[i]
		}
		main = sizes[rng.IntN(len(sizes))]
		c.aboveFold = rng.Float64() < 0.3
	}
	if c.devices == 0 {
		for c.devices == 0 {
			for _, d := range []shape.Device{shape.Desktop, shape.Mobile, shape.Tablet} {
				if rng.IntN(2) == 0 {
					c.devices |= 1 << d
				}
			}
		}
	}
	if rng.Float64() >= 0.2 {
		for range 1 + rng.IntN(4) {
			c.countries |= 1 << randCountry(rng)
		}
	}
	switch r := rng.Float64(); {
	case r < 0.15:
		c.channels = 1
	case r < 0.30:
		c.channels = 2
	}
	if rng.Float64() < 0.2 {
		c.pubs = map[uint32]bool{}
		for range 10 + rng.IntN(31) {
			c.pubs[uint32(1+rng.IntN(w.sc.Publishers))] = true
		}
	}
	c.p = math.Exp(math.Log(0.03) + rng.Float64()*(math.Log(0.35)-math.Log(0.03)))
	c.price = formatCPM[main] * math.Exp(0.3*rng.NormFloat64())
	return c
}

// expect returns a buyer's true chance of bidding on an impression and its
// expected price if it does.
func (w *World) expect(b int, f shape.Features, quality, t float64) (p, mu float64) {
	by := &w.buyers[b]
	noBid := 1 - by.base
	wsum := by.base * 0.3 * formatCPM[f.Format]
	psum := by.base
	qp := math.Sqrt(quality)
	for i := range by.camps {
		c := &by.camps[i]
		if !c.matches(f, t) {
			continue
		}
		pc := math.Min(0.95, c.p*qp)
		noBid *= 1 - pc
		wsum += pc * c.price
		psum += pc
	}
	return 1 - noBid, wsum / psum * quality * deviceMult(f.Device)
}

const (
	saltBid   = 0xb1d5a17
	saltPrice = 0x9a1ce
)

// realize turns an expectation into the buyer's actual answer. It depends
// only on the impression and the buyer, never on the policy, so every
// policy faces the same answers.
func realize(impID uint64, b int, p, mu float64) float64 {
	h := shape.Mix2(impID^saltBid, uint64(b))
	if shape.Unit(h) >= p {
		return 0
	}
	u1 := math.Max(1e-12, shape.Unit(shape.Mix2(h, saltPrice)))
	u2 := shape.Unit(shape.Mix2(h, saltPrice+1))
	z := math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
	const sigma = 0.5
	return mu * math.Exp(sigma*z-sigma*sigma/2)
}
