package router

import (
	"math"
	"math/rand"
	"sort"
)

/**
 * The statistics §9 asks the report for: medians with a bootstrap
 * confidence interval, a nearest-rank p90, and the two-proportion
 * z-test. Everything is deterministic — the bootstrap reseeds itself —
 * so synthetic-fixture tests can assert exact numbers.
 */

// median of xs (empty → 0). Even lengths average the middle pair.
func median(xs []float64) float64 {
	n := len(xs)
	if n == 0 {
		return 0
	}
	ys := append([]float64(nil), xs...)
	sort.Float64s(ys)
	if n%2 == 1 {
		return ys[n/2]
	}
	return (ys[n/2-1] + ys[n/2]) / 2
}

// mean of xs (empty → 0).
func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// p90 by nearest rank: the ⌈0.9·n⌉-th smallest value (empty → 0).
func p90(xs []float64) float64 {
	n := len(xs)
	if n == 0 {
		return 0
	}
	ys := append([]float64(nil), xs...)
	sort.Float64s(ys)
	rank := int(math.Ceil(0.9 * float64(n)))
	if rank < 1 {
		rank = 1
	}
	return ys[rank-1]
}

// bootstrapMedianCI resamples xs (with replacement) n times and returns
// the 2.5th and 97.5th percentile of the medians. The seed is fixed so
// repeated runs — and tests — see the same interval.
func bootstrapMedianCI(xs []float64, n int) (lo, hi float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	rng := rand.New(rand.NewSource(20261005))
	medians := make([]float64, n)
	for i := range medians {
		sample := make([]float64, len(xs))
		for j := range sample {
			sample[j] = xs[rng.Intn(len(xs))]
		}
		medians[i] = median(sample)
	}
	sort.Float64s(medians)
	lo = medians[int(0.025*float64(n))]
	hi = medians[int(0.975*float64(n))-1]
	if hi < lo {
		lo, hi = hi, lo
	}
	return lo, hi
}

// twoProportionZ compares p1 = x1/n1 with p2 = x2/n2 (normal
// approximation, pooled). n1 or n2 zero → 0 (no evidence either way).
func twoProportionZ(x1, n1, x2, n2 int) float64 {
	if n1 == 0 || n2 == 0 {
		return 0
	}
	p1 := float64(x1) / float64(n1)
	p2 := float64(x2) / float64(n2)
	p := (float64(x1) + float64(x2)) / float64(n1+n2)
	se := math.Sqrt(p * (1 - p) * (1/float64(n1) + 1/float64(n2)))
	if se == 0 {
		return 0
	}
	return (p1 - p2) / se
}
