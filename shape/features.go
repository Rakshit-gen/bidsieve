// Package shape decides which bid requests an exchange forwards to which
// buyer when every buyer caps the requests per second it will accept.
//
// The usual way to do this is to keep a bid rate per request "tuple" and
// stop sending the tuples that don't get bids. That works until a buyer
// changes its mind: a tuple you stopped sending can never show you that it
// got good again. This package keeps the same kind of per-tuple statistics,
// but lets old evidence fade, turns faded evidence into uncertainty, and
// spends a small, priced share of each buyer's cap on finding out.
package shape

import "fmt"

// Format is the kind of ad slot.
type Format uint8

const (
	BannerOther Format = iota
	Banner300x250
	Banner728x90
	Banner320x50
	Banner160x600
	Video
	Native
	numFormats
)

var formatNames = [...]string{"banner", "300x250", "728x90", "320x50", "160x600", "video", "native"}

func (f Format) String() string {
	if int(f) < len(formatNames) {
		return formatNames[f]
	}
	return fmt.Sprintf("format(%d)", f)
}

// Device follows the coarse split buyers actually target on.
type Device uint8

const (
	DeviceUnknown Device = iota
	Desktop
	Mobile
	Tablet
	CTV
	numDevices
)

var deviceNames = [...]string{"unknown", "desktop", "mobile", "tablet", "ctv"}

func (d Device) String() string {
	if int(d) < len(deviceNames) {
		return deviceNames[d]
	}
	return fmt.Sprintf("device(%d)", d)
}

// Channel is where the ad shows: a website or an app (CTV apps count as apps).
type Channel uint8

const (
	Web Channel = iota
	App
)

func (c Channel) String() string {
	if c == App {
		return "app"
	}
	return "web"
}

// Position is the slot position as OpenRTB reports it, reduced to what matters.
type Position uint8

const (
	PosUnknown Position = iota
	AboveFold
	BelowFold
	PosOther
)

var posNames = [...]string{"unknown", "above", "below", "other"}

func (p Position) String() string {
	if int(p) < len(posNames) {
		return posNames[p]
	}
	return fmt.Sprintf("pos(%d)", p)
}

// Features are the request fields the shaper learns from. They match the
// tuple most exchanges and Amazon's Dynamic Traffic Engine use: channel,
// format, publisher, country, slot position and device.
//
// Publisher and Country are dense indexes the caller assigns. Use an
// Interner for publishers and CountryIndex for ISO codes.
type Features struct {
	Publisher uint32 // below 1<<24
	Format    Format
	Country   uint8 // below 64, 0 means unknown
	Device    Device
	Channel   Channel
	Pos       Position
}

const maxPublisher = 1 << 24

// Valid reports whether every field fits the packed key.
func (f Features) Valid() bool {
	return f.Publisher < maxPublisher && f.Format < 16 && f.Country < 64 &&
		f.Device < 8 && f.Channel < 2 && f.Pos < 4
}

func (f Features) String() string {
	return fmt.Sprintf("%s|%s|pub%d|c%d|%s|%s", f.Channel, f.Format, f.Publisher, f.Country, f.Pos, f.Device)
}

// Bit layout of a packed key:
//
//	0-23  publisher
//	24-27 format
//	28-33 country
//	34-36 device
//	37    channel
//	38-39 position
//	40-42 level
//	43-48 buyer
func (f Features) pack() uint64 {
	return uint64(f.Publisher) |
		uint64(f.Format)<<24 |
		uint64(f.Country)<<28 |
		uint64(f.Device)<<34 |
		uint64(f.Channel)<<37 |
		uint64(f.Pos)<<38
}

const (
	maskFormat  = uint64(0xF) << 24
	maskCountry = uint64(0x3F) << 28
	maskDevice  = uint64(0x7) << 34
	maskChannel = uint64(1) << 37
	maskPos     = uint64(0x3) << 38
	maskPub     = uint64(maxPublisher - 1)
)

// Levels go from coarse to fine. A request's estimate starts at the buyer's
// overall bid rate and is refined one level at a time, so a publisher the
// shaper has never seen still gets a sensible estimate from its format,
// country and device.
const numLevels = 6

var levelMasks = [numLevels]uint64{
	0,
	maskFormat,
	maskFormat | maskCountry,
	maskFormat | maskCountry | maskDevice,
	maskFormat | maskCountry | maskDevice | maskChannel | maskPos,
	maskFormat | maskCountry | maskDevice | maskChannel | maskPos | maskPub,
}

// LevelNames describes each level, for reports.
var LevelNames = [numLevels]string{"buyer", "format", "+country", "+device", "+channel,pos", "+publisher"}

const finest = numLevels - 1

func cellKey(dsp int, level int, packed uint64) uint64 {
	return packed&levelMasks[level] | uint64(level)<<40 | uint64(dsp)<<43
}

// SegmentOf returns the features with the publisher, channel and position
// cleared. Reports group withheld traffic this way, because a list of single
// publishers is too fine to act on.
func SegmentOf(f Features) Features {
	return Features{Format: f.Format, Country: f.Country, Device: f.Device}
}
