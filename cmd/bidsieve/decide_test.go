package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Rakshit-gen/bidsieve/shape"
)

func TestDecideSample(t *testing.T) {
	in, err := os.Open("testdata/requests.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	cfg := shape.DefaultConfig()
	cfg.Buyers, _ = parseBuyers("a:100,b:5")
	s, err := shape.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	at := time.Unix(1_700_000_000, 0)
	if err := decide(s, in, &out, false, func() time.Time { return at }); err != nil {
		t.Fatal(err)
	}
	var lines []decisionLine
	sc := bufio.NewScanner(&out)
	for sc.Scan() {
		var d decisionLine
		if err := json.Unmarshal(sc.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, d)
	}
	// Three valid requests with one impression each, two buyers; the
	// fourth line is junk and is skipped.
	if len(lines) != 6 {
		t.Fatalf("got %d decision lines, want 6", len(lines))
	}
	// Line 3 is the same app impression as line 2 through another path.
	// Whoever got line 2 must not get it again.
	for b := range 2 {
		if lines[2+b].Send && lines[4+b].Send {
			t.Fatalf("buyer %s got the copy on line 3 as well", lines[4+b].Buyer)
		}
	}
}

func TestParseBuyers(t *testing.T) {
	bs, err := parseBuyers("x:10, y:20")
	if err != nil || len(bs) != 2 || bs[1].Name != "y" || bs[1].QPS != 20 {
		t.Fatalf("got %v, %v", bs, err)
	}
	for _, bad := range []string{"x", "x:", ":5", "x:abc"} {
		if _, err := parseBuyers(bad); err == nil {
			t.Fatalf("%q parsed", bad)
		}
	}
}
