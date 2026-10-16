package shape

import "testing"

const sampleBidRequest = `{
  "id": "80ce30c53c16e6ede735f123ef6e32361bfc7b22",
  "imp": [
    {"id": "1", "tagid": "top", "banner": {"w": 300, "h": 250, "pos": 1}},
    {"id": "2", "tagid": "pre", "video": {"pos": 3}, "ext": {"tid": "imp-tid-2"}}
  ],
  "site": {"id": "102855", "page": "http://www.foobar.com/1234.html", "publisher": {"id": "8953"}},
  "device": {"devicetype": 2, "ip": "203.0.113.7", "ua": "Mozilla/5.0", "geo": {"country": "DEU"}},
  "source": {"tid": "shared-tid"}
}`

func TestFromOpenRTB(t *testing.T) {
	var pubs Interner
	reqs, err := FromOpenRTB([]byte(sampleBidRequest), &pubs, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 2 {
		t.Fatalf("got %d requests, want one per imp", len(reqs))
	}
	b, v := reqs[0].Features, reqs[1].Features
	if b.Format != Banner300x250 || b.Pos != AboveFold || b.Device != Desktop || b.Channel != Web {
		t.Fatalf("banner features: %v", b)
	}
	if CountryName(b.Country) != "DEU" {
		t.Fatalf("country %d", b.Country)
	}
	if v.Format != Video || v.Pos != BelowFold {
		t.Fatalf("video features: %v", v)
	}
	if b.Publisher != v.Publisher || b.Publisher == 0 {
		t.Fatalf("both imps belong to publisher 8953: %d %d", b.Publisher, v.Publisher)
	}
	if reqs[0].Dupe.Fingerprint == reqs[1].Dupe.Fingerprint {
		t.Fatal("two slots on one page must not share a fingerprint")
	}
	if reqs[0].Dupe.TID == 0 || reqs[0].Dupe.TID == reqs[1].Dupe.TID {
		t.Fatal("trusted tids should be set and differ per imp")
	}
	if reqs[0].ID == reqs[1].ID {
		t.Fatal("request IDs must differ per imp")
	}

	untrusted, _ := FromOpenRTB([]byte(sampleBidRequest), &pubs, false)
	if untrusted[0].Dupe.TID != 0 {
		t.Fatal("untrusted tids must be ignored")
	}
	if untrusted[0].Features.Publisher != b.Publisher {
		t.Fatal("the interner should return the same index for the same publisher")
	}
}

func TestFromOpenRTBRejectsJunk(t *testing.T) {
	var pubs Interner
	if _, err := FromOpenRTB([]byte(`{"id":"x","imp":[]}`), &pubs, false); err == nil {
		t.Fatal("no imps should be an error")
	}
	if _, err := FromOpenRTB([]byte(`not json`), &pubs, false); err == nil {
		t.Fatal("bad json should be an error")
	}
}
