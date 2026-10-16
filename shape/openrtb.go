package shape

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Countries the shaper keeps apart, as OpenRTB alpha-3 codes. Index 0 is
// everything else. The list is short on purpose: past a few dozen countries
// the tail is too thin to learn per country anyway, and the levels above
// catch it.
var Countries = []string{"", "USA", "GBR", "DEU", "FRA", "IND", "BRA", "JPN", "CAN", "AUS", "ESP", "ITA", "MEX",
	"NLD", "SWE", "POL", "TUR", "IDN", "PHL", "VNM", "THA", "KOR", "ARG", "COL", "ZAF", "NGA", "EGY", "SAU", "ARE"}

var countryIndex = func() map[string]uint8 {
	m := make(map[string]uint8, len(Countries))
	for i, c := range Countries {
		if c != "" {
			m[c] = uint8(i)
		}
	}
	return m
}()

// CountryIndex maps an alpha-3 code to its index, or 0 if it isn't listed.
func CountryIndex(code string) uint8 { return countryIndex[strings.ToUpper(code)] }

// CountryName is the reverse of CountryIndex.
func CountryName(i uint8) string {
	if int(i) < len(Countries) && Countries[i] != "" {
		return Countries[i]
	}
	return "other"
}

// Interner hands out dense publisher indexes. It is safe for concurrent use.
type Interner struct {
	mu sync.RWMutex
	m  map[string]uint32
}

// Index returns the publisher's index, assigning the next one if it's new.
// Index 0 is reserved for requests with no publisher ID.
func (in *Interner) Index(id string) uint32 {
	if id == "" {
		return 0
	}
	in.mu.RLock()
	i, ok := in.m[id]
	in.mu.RUnlock()
	if ok {
		return i
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.m == nil {
		in.m = map[string]uint32{}
	}
	if i, ok := in.m[id]; ok {
		return i
	}
	i = uint32(len(in.m) + 1)
	if i >= maxPublisher {
		// Out of room: fold the rest into one bucket rather than collide
		// with a real publisher.
		return maxPublisher - 1
	}
	in.m[id] = i
	return i
}

// The subset of an OpenRTB 2.6 bid request the shaper reads.
type ortbRequest struct {
	ID     string `json:"id"`
	Imp    []ortbImp
	Site   *ortbProperty `json:"site"`
	App    *ortbProperty `json:"app"`
	Device struct {
		DeviceType int    `json:"devicetype"`
		IFA        string `json:"ifa"`
		IP         string `json:"ip"`
		UA         string `json:"ua"`
		Geo        struct {
			Country string `json:"country"`
		} `json:"geo"`
	} `json:"device"`
	User struct {
		ID string `json:"id"`
	} `json:"user"`
	Source struct {
		TID string `json:"tid"`
	} `json:"source"`
}

type ortbProperty struct {
	ID        string `json:"id"`
	Page      string `json:"page"`
	Bundle    string `json:"bundle"`
	Publisher struct {
		ID string `json:"id"`
	} `json:"publisher"`
}

type ortbImp struct {
	ID     string `json:"id"`
	TagID  string `json:"tagid"`
	Banner *struct {
		W   int `json:"w"`
		H   int `json:"h"`
		Pos int `json:"pos"`
	} `json:"banner"`
	Video *struct {
		Pos int `json:"pos"`
	} `json:"video"`
	Native *struct{} `json:"native"`
	Ext    struct {
		TID string `json:"tid"`
	} `json:"ext"`
}

// FromOpenRTB turns an OpenRTB 2.6 bid request into one Request per
// impression. Set trustTID only if your wrappers share source.tid across
// every path; otherwise duplicates are matched on the fingerprint alone.
// The request IDs are hashed from the bid request ID and the imp ID.
func FromOpenRTB(body []byte, pubs *Interner, trustTID bool) ([]Request, error) {
	var br ortbRequest
	if err := json.Unmarshal(body, &br); err != nil {
		return nil, fmt.Errorf("shape: bad bid request: %w", err)
	}
	if len(br.Imp) == 0 {
		return nil, fmt.Errorf("shape: bid request %q has no impressions", br.ID)
	}
	prop, ch := br.Site, Web
	if br.App != nil {
		prop, ch = br.App, App
	}
	var pubID, page string
	if prop != nil {
		pubID = prop.Publisher.ID
		if pubID == "" {
			pubID = prop.ID
		}
		page = prop.Page
		if ch == App {
			page = prop.Bundle
		}
	}
	dev := br.Device.IFA
	if dev == "" {
		dev = br.User.ID
	}
	if dev == "" {
		dev = br.Device.IP + "|" + br.Device.UA
	}
	base := Features{
		Publisher: pubs.Index(pubID),
		Country:   CountryIndex(br.Device.Geo.Country),
		Device:    deviceFromORTB(br.Device.DeviceType),
		Channel:   ch,
	}
	out := make([]Request, 0, len(br.Imp))
	for _, imp := range br.Imp {
		f := base
		switch {
		case imp.Video != nil:
			f.Format, f.Pos = Video, posFromORTB(imp.Video.Pos)
		case imp.Native != nil:
			f.Format = Native
		case imp.Banner != nil:
			f.Format, f.Pos = bannerFormat(imp.Banner.W, imp.Banner.H), posFromORTB(imp.Banner.Pos)
		}
		slot := imp.TagID
		if slot == "" {
			slot = imp.ID
		}
		r := Request{
			ID:       Mix2(HashString(br.ID), HashString(imp.ID)),
			Features: f,
			Dupe:     DupeKey{Fingerprint: Fingerprint(pubID, page, slot, dev)},
		}
		if trustTID {
			tid := imp.Ext.TID
			if tid == "" {
				tid = br.Source.TID
			}
			if tid != "" {
				r.Dupe.TID = HashString(tid + "|" + imp.ID)
			}
		}
		out = append(out, r)
	}
	return out, nil
}

func deviceFromORTB(t int) Device {
	switch t {
	case 2:
		return Desktop
	case 1, 4:
		return Mobile
	case 5:
		return Tablet
	case 3, 7:
		return CTV
	}
	return DeviceUnknown
}

func posFromORTB(p int) Position {
	switch p {
	case 0:
		return PosUnknown
	case 1:
		return AboveFold
	case 3:
		return BelowFold
	}
	return PosOther
}

func bannerFormat(w, h int) Format {
	switch {
	case w == 300 && h == 250:
		return Banner300x250
	case w == 728 && h == 90:
		return Banner728x90
	case w == 320 && h == 50:
		return Banner320x50
	case w == 160 && h == 600:
		return Banner160x600
	}
	return BannerOther
}
