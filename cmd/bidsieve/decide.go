package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Rakshit-gen/bidsieve/shape"
)

// decideCmd reads OpenRTB 2.x bid requests from stdin, one JSON object per
// line, and writes one line per impression and buyer saying whether the
// shaper would forward it. The model starts empty, so on its own this shows
// the cold-start behaviour and checks that real requests parse; in an
// exchange the same calls sit next to an Observe for every answer.
func decideCmd(args []string) {
	fs := flag.NewFlagSet("decide", flag.ExitOnError)
	buyers := fs.String("buyers", "dsp-a:5000,dsp-b:800", "buyers as name:qps, comma separated")
	trustTID := fs.Bool("trust-tid", false, "treat source.tid as shared across every path to the exchange")
	fs.Parse(args)

	cfg := shape.DefaultConfig()
	var err error
	if cfg.Buyers, err = parseBuyers(*buyers); err != nil {
		fatal(err)
	}
	s, err := shape.New(cfg)
	if err != nil {
		fatal(err)
	}
	if err := decide(s, os.Stdin, os.Stdout, *trustTID, time.Now); err != nil {
		fatal(err)
	}
}

func parseBuyers(spec string) ([]shape.Buyer, error) {
	var out []shape.Buyer
	for _, part := range strings.Split(spec, ",") {
		name, qps, ok := strings.Cut(strings.TrimSpace(part), ":")
		n, err := strconv.Atoi(qps)
		if !ok || name == "" || err != nil {
			return nil, fmt.Errorf("bad buyer %q, want name:qps", part)
		}
		out = append(out, shape.Buyer{Name: name, QPS: n})
	}
	return out, nil
}

type decisionLine struct {
	Line   int     `json:"line"`
	Imp    int     `json:"imp"`
	Buyer  string  `json:"buyer"`
	Send   bool    `json:"send"`
	Reason string  `json:"reason"`
	Prop   float64 `json:"prop"`
	Value  float64 `json:"value"`
}

func decide(s *shape.Shaper, in io.Reader, out io.Writer, trustTID bool, now func() time.Time) error {
	var pubs shape.Interner
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	enc := json.NewEncoder(out)
	var decs []shape.Decision
	names := s.Buyers()
	for line := 1; sc.Scan(); line++ {
		body := sc.Bytes()
		if len(strings.TrimSpace(string(body))) == 0 {
			continue
		}
		reqs, err := shape.FromOpenRTB(body, &pubs, trustTID)
		if err != nil {
			// One bad request shouldn't stop the stream; say which line it was.
			fmt.Fprintf(os.Stderr, "line %d: %v\n", line, err)
			continue
		}
		t := now()
		for i := range reqs {
			decs = s.Decide(&reqs[i], t, decs)
			for _, d := range decs {
				err := enc.Encode(decisionLine{line, i, names[d.Buyer].Name, d.Send, d.Reason.String(), d.Prop, d.Value})
				if err != nil {
					return err
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return fmt.Errorf("a request is longer than 4 MB: %w", err)
		}
		return err
	}
	return nil
}
