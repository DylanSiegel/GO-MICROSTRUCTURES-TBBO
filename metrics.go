package main

import (
	"math"
)

// RobustStats accumulates sums for online calculation of IC, T-Stats, and Sharpe proxy.
type RobustStats struct {
	Count    int
	SumSig   float64
	SumRet   float64
	SumSqSig float64
	SumSqRet float64
	SumProd  float64 // Sum(Signal * Return)
}

type AlphaResult struct {
	IC        float64
	TStat     float64
	Sharpe    float64 // Annualized proxy
	Stability float64 // reserved
}

// UpdateInline adds a data point.
// Go 1.25 compiler inlines this aggressively into the hot loop in math.go.
func (m *RobustStats) Update(s, r float64) {
	if math.IsNaN(s) || math.IsNaN(r) {
		return
	}
	m.Count++
	m.SumSig += s
	m.SumRet += r
	m.SumSqSig += s * s
	m.SumSqRet += r * r
	m.SumProd += s * r
}

func (m *RobustStats) Merge(other RobustStats) {
	m.Count += other.Count
	m.SumSig += other.SumSig
	m.SumRet += other.SumRet
	m.SumSqSig += other.SumSqSig
	m.SumSqRet += other.SumSqRet
	m.SumProd += other.SumProd
}

func (m *RobustStats) Calculate() AlphaResult {
	n := float64(m.Count)
	if n < 30 {
		return AlphaResult{}
	}

	// Pearson Correlation Formula:
	// Cov(X,Y) / (Std(X) * Std(Y))

	meanSig := m.SumSig / n
	meanRet := m.SumRet / n

	cov := (m.SumProd / n) - (meanSig * meanRet)
	varSig := (m.SumSqSig / n) - (meanSig * meanSig)
	varRet := (m.SumSqRet / n) - (meanRet * meanRet)

	ic := 0.0
	if varSig > 0 && varRet > 0 {
		ic = cov / math.Sqrt(varSig*varRet)
	}

	// T-Stat Calculation
	// t = r * sqrt(n-2) / sqrt(1-r^2)
	tStat := 0.0
	// Avoid division by zero if perfect correlation (rare but possible)
	if math.Abs(ic) < 0.999999 {
		tStat = ic * math.Sqrt(n-2) / math.Sqrt(1.0-(ic*ic))
	} else if ic > 0 {
		tStat = 999.0
	} else {
		tStat = -999.0
	}

	// Sharpe Proxy (Annualized)
	// Assuming ~1440 mins * 252 days if horizon was 1m.
	// This is just a scaling factor for relative comparison.
	sharpe := ic * math.Sqrt(252*1440)

	return AlphaResult{
		IC:     ic,
		TStat:  tStat,
		Sharpe: sharpe,
	}
}
