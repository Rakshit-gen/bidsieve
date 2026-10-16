// Package ope estimates how a shaping policy would have done, using only
// the decision logs of a different policy.
//
// An exchange can't A/B test a new shaper casually: the arm that sends the
// wrong requests loses real money for as long as the test runs. If the live
// shaper logs the probability it had of sending each request, those logs
// are enough to score any other policy offline. The estimators here are the
// standard ones, written to run as a stream so a day of logs never has to
// sit in memory.
package ope

import "math"

// Sample is one logged (request, buyer) decision.
type Sample struct {
	P         float64 // chance the logging policy had of sending it
	Q         float64 // chance the target policy would send it
	Sent      bool    // whether it actually went
	Reward    float64 // what came back if it went: the bid price, or 0
	Predicted float64 // the logging model's expected reward, for DR
}

// Estimator accumulates samples for one target policy.
type Estimator struct {
	n int64

	ips, ips2 float64
	dr, dr2   float64

	wsum, wr float64 // self-normalised IPS
	volume   float64 // sum of Q: requests the target would send

	naiveR, naiveQ float64

	unsupported float64 // target sends the logging policy could never have made
}

// Add records one sample.
func (e *Estimator) Add(s Sample) {
	e.n++
	e.volume += s.Q
	if s.P <= 0 {
		if s.Q > 0 {
			e.unsupported += s.Q
		}
		e.dr += s.Q * s.Predicted
		e.dr2 += (s.Q * s.Predicted) * (s.Q * s.Predicted)
		return
	}
	var x, w float64
	if s.Sent {
		w = s.Q / s.P
		x = w * s.Reward
		e.naiveR += s.Q * s.Reward
		e.naiveQ += s.Q
	}
	e.ips += x
	e.ips2 += x * x
	e.wsum += w
	e.wr += x
	d := s.Q*s.Predicted + x - w*s.Predicted
	e.dr += d
	e.dr2 += d * d
}

// Result is a total with its standard error.
type Result struct {
	Total  float64
	StdErr float64
}

func total(sum, sum2 float64, n int64) Result {
	if n == 0 {
		return Result{}
	}
	mean := sum / float64(n)
	v := sum2/float64(n) - mean*mean
	return Result{Total: sum, StdErr: math.Sqrt(math.Max(0, v) * float64(n))}
}

// IPS is inverse propensity scoring: unbiased whenever P > 0 wherever Q > 0,
// and noisy when P is small.
func (e *Estimator) IPS() Result { return total(e.ips, e.ips2, e.n) }

// DR is doubly robust: it starts from the logging model's prediction and
// uses the logged outcomes only to correct it. Unbiased if either the
// propensities or the predictions are right, and usually much tighter.
func (e *Estimator) DR() Result { return total(e.dr, e.dr2, e.n) }

// SNIPS divides by the realised weights instead of their expectation.
// Slightly biased, much steadier when a few samples have huge weights.
func (e *Estimator) SNIPS() float64 {
	if e.wsum == 0 {
		return 0
	}
	return e.wr / e.wsum * e.volume
}

// Naive is what people often do instead: average the reward over the
// logged requests the target would also send, then scale by the target's
// volume. It ignores that the logged requests were picked for looking good,
// so it overstates any policy that sends more than the logger did.
func (e *Estimator) Naive() float64 {
	if e.naiveQ == 0 {
		return 0
	}
	return e.naiveR / e.naiveQ * e.volume
}

// Volume is the number of requests the target policy would send.
func (e *Estimator) Volume() float64 { return e.volume }

// Unsupported is the share of the target's sends that the logging policy
// had zero chance of making. IPS can't see that traffic at all.
func (e *Estimator) Unsupported() float64 {
	if e.volume == 0 {
		return 0
	}
	return e.unsupported / e.volume
}

// N is the number of samples.
func (e *Estimator) N() int64 { return e.n }
