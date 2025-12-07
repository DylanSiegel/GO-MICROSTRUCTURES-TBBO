package main

import (
	"math"
	"sort"
	"sync"
)

// ============================================================================
//  ASSET CONFIGURATION
// ============================================================================

type AssetConfig struct {
	Symbol        string
	TickValue     float64
	CostPerTrade  float64
	BpsMultiplier float64
}

var AssetConfigs = map[string]AssetConfig{
	"MES": {Symbol: "MES", TickValue: 1.25, CostPerTrade: 0.62, BpsMultiplier: 2.50},
	"MNQ": {Symbol: "MNQ", TickValue: 0.50, CostPerTrade: 0.62, BpsMultiplier: 2.00},
	"MGC": {Symbol: "MGC", TickValue: 1.00, CostPerTrade: 1.62, BpsMultiplier: 10.0},
}

func GetAssetConfig(sym string) AssetConfig {
	if c, ok := AssetConfigs[sym]; ok {
		return c
	}
	return AssetConfig{Symbol: sym, TickValue: 1.0, CostPerTrade: 0.0, BpsMultiplier: 1.0}
}

// ============================================================================
//  TRADE-LEVEL / STRATEGY-LEVEL RISK METRICS (per signal × horizon)
// ============================================================================

// To avoid unbounded RAM growth we cap how many per-trade returns
// and signal/return samples we keep for tail metrics / IC.
const (
	maxReturnsPerStat = 100_000
	maxICSamples      = 100_000
)

// Here, each (signal, horizon) observation is treated as one "pseudo-trade":
// - Position direction = sign(signal)
// - Return = sign(signal) * future log-return
// Fees = 0 (this is pure alpha / information evaluation).

type AdvancedStats struct {
	Count     int
	Wins      int
	TotalPnL  float64 // sum of net returns (strategy returns)
	TotalFees float64 // always 0 in pure-alpha mode

	MaxDD   float64
	PeakPnL float64

	PnL_Markout float64 // sum of "markout" returns (same as TotalPnL here)
	PnL_Real    float64 // same as TotalPnL

	SumPnL  float64
	SumPnL2 float64
	SumPnL3 float64
	SumPnL4 float64

	Returns []float64 // capped; used for tails / W/L ratio
}

func (s *AdvancedStats) Update(markout, retReal, fee float64) {
	s.Count++
	net := retReal - fee

	s.TotalPnL += net
	s.TotalFees += fee
	s.PnL_Markout += markout
	s.PnL_Real += net

	if net > 0 {
		s.Wins++
	}
	if s.TotalPnL > s.PeakPnL {
		s.PeakPnL = s.TotalPnL
	}
	dd := s.PeakPnL - s.TotalPnL
	if dd > s.MaxDD {
		s.MaxDD = dd
	}

	s.SumPnL += net
	s.SumPnL2 += net * net
	s.SumPnL3 += net * net * net
	s.SumPnL4 += net * net * net * net

	// Bound memory: keep only first maxReturnsPerStat samples.
	if len(s.Returns) < maxReturnsPerStat {
		s.Returns = append(s.Returns, net)
	}
}

func (s *AdvancedStats) WinRate() float64 {
	if s.Count == 0 {
		return 0
	}
	return float64(s.Wins) / float64(s.Count) * 100.0
}

func (s *AdvancedStats) Skewness() float64 {
	if s.Count < 3 {
		return 0
	}
	n := float64(s.Count)
	mean := s.SumPnL / n
	variance := (s.SumPnL2 / n) - (mean * mean)
	if variance < 1e-12 {
		return 0
	}
	stdDev := math.Sqrt(variance)
	m3 := (s.SumPnL3 / n) - (3 * mean * (s.SumPnL2 / n)) + (2 * mean * mean * mean)
	return m3 / (stdDev * stdDev * stdDev)
}

func (s *AdvancedStats) Sharpe() float64 {
	if s.Count < 2 {
		return 0
	}
	n := float64(s.Count)
	mean := s.SumPnL / n
	variance := (s.SumPnL2 / n) - mean*mean
	if variance <= 1e-12 {
		return 0
	}
	stdDev := math.Sqrt(variance)
	return mean / stdDev
}

func (s *AdvancedStats) WinLossRatio() float64 {
	var sumWin, sumLoss float64
	var nWin, nLoss int

	for _, r := range s.Returns {
		if r > 0 {
			sumWin += r
			nWin++
		} else if r < 0 {
			sumLoss += r
			nLoss++
		}
	}
	if nWin == 0 || nLoss == 0 {
		return 0
	}
	avgWin := sumWin / float64(nWin)
	avgLoss := sumLoss / float64(nLoss)
	if avgLoss == 0 {
		return 0
	}
	return math.Abs(avgWin / avgLoss)
}

func (s *AdvancedStats) TailPercentile(p float64) float64 {
	n := len(s.Returns)
	if n == 0 {
		return 0
	}
	if p <= 0 {
		p = 0
	}
	if p >= 1 {
		p = 1
	}
	cp := make([]float64, n)
	copy(cp, s.Returns)
	sort.Float64s(cp)
	idx := int(p * float64(n-1))
	return cp[idx]
}

// ============================================================================
//  SIGNAL / RETURN JOINT METRICS (per signal × horizon)
// ============================================================================

type ICStats struct {
	Sig []float64
	Ret []float64
}

func (s *ICStats) Observe(sig, ret float64) {
	if math.IsNaN(sig) || math.IsNaN(ret) {
		return
	}
	// Bound memory: keep at most maxICSamples
	if len(s.Sig) < maxICSamples {
		s.Sig = append(s.Sig, sig)
		s.Ret = append(s.Ret, ret)
	}
}

func (s *ICStats) Count() int {
	return len(s.Sig)
}

// Backwards-compatible alias for Pearson IC.
func (s *ICStats) IC() float64 {
	return s.PearsonIC()
}

// 1. Pearson IC
func (s *ICStats) PearsonIC() float64 {
	n := len(s.Sig)
	if n < 2 || len(s.Ret) != n {
		return 0
	}
	return pearsonFromSamples(s.Sig, s.Ret)
}

// 1b. Rank IC (Spearman)
func (s *ICStats) RankIC() float64 {
	n := len(s.Sig)
	if n < 2 || len(s.Ret) != n {
		return 0
	}

	type pair struct {
		v   float64
		idx int
	}

	sPairs := make([]pair, n)
	for i, v := range s.Sig {
		sPairs[i] = pair{v, i}
	}
	sort.Slice(sPairs, func(i, j int) bool { return sPairs[i].v < sPairs[j].v })
	rSig := make([]float64, n)
	for rank, p := range sPairs {
		rSig[p.idx] = float64(rank + 1)
	}

	rPairs := make([]pair, n)
	for i, v := range s.Ret {
		rPairs[i] = pair{v, i}
	}
	sort.Slice(rPairs, func(i, j int) bool { return rPairs[i].v < rPairs[j].v })
	rRet := make([]float64, n)
	for rank, p := range rPairs {
		rRet[p.idx] = float64(rank + 1)
	}

	return pearsonFromSamples(rSig, rRet)
}

func pearsonFromSamples(x, y []float64) float64 {
	n := len(x)
	if n < 2 || len(y) != n {
		return 0
	}

	var sumX, sumY, sumX2, sumY2, sumXY float64
	for i := 0; i < n; i++ {
		xi := x[i]
		yi := y[i]
		sumX += xi
		sumY += yi
		sumX2 += xi * xi
		sumY2 += yi * yi
		sumXY += xi * yi
	}
	nf := float64(n)
	cov := (sumXY / nf) - (sumX/nf)*(sumY/nf)
	varX := (sumX2 / nf) - (sumX/nf)*(sumX/nf)
	varY := (sumY2 / nf) - (sumY/nf)*(sumY/nf)
	if varX <= 1e-12 || varY <= 1e-12 {
		return 0
	}
	return cov / math.Sqrt(varX*varY)
}

// 2. Hit rate sign(S) vs sign(R)
func (s *ICStats) HitRate() float64 {
	n := len(s.Sig)
	if n == 0 || len(s.Ret) != n {
		return 0
	}
	var total, correct int
	for i := 0; i < n; i++ {
		sig := s.Sig[i]
		ret := s.Ret[i]
		if ret == 0 || sig == 0 {
			continue
		}
		signSig := 1
		if sig < 0 {
			signSig = -1
		}
		signRet := 1
		if ret < 0 {
			signRet = -1
		}
		if signSig == signRet {
			correct++
		}
		total++
	}
	if total == 0 {
		return 0
	}
	return float64(correct) / float64(total)
}

// 3. Decile conditional return curve
func (s *ICStats) DecileCurve(K int) (avgRet []float64, counts []int) {
	n := len(s.Sig)
	if n == 0 || len(s.Ret) != n {
		return nil, nil
	}
	if K <= 0 {
		K = 10
	}

	type pair struct {
		sig float64
		ret float64
	}
	arr := make([]pair, n)
	for i := 0; i < n; i++ {
		arr[i] = pair{s.Sig[i], s.Ret[i]}
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i].sig < arr[j].sig })

	sums := make([]float64, K)
	counts = make([]int, K)
	for idx, p := range arr {
		d := int(float64(idx) * float64(K) / float64(n))
		if d == K {
			d = K - 1
		}
		sums[d] += p.ret
		counts[d]++
	}
	avgRet = make([]float64, K)
	for k := 0; k < K; k++ {
		if counts[k] > 0 {
			avgRet[k] = sums[k] / float64(counts[k])
		}
	}
	return avgRet, counts
}

// 4. Mutual Information / NMI
func (s *ICStats) MutualInformation(sigBins, retBins int) (mi, nmi float64) {
	n := len(s.Sig)
	if n == 0 || len(s.Ret) != n {
		return 0, 0
	}
	if sigBins <= 0 {
		sigBins = 10
	}
	if retBins <= 0 {
		retBins = 3
	}

	minS, maxS := s.Sig[0], s.Sig[0]
	minR, maxR := s.Ret[0], s.Ret[0]
	for i := 1; i < n; i++ {
		if s.Sig[i] < minS {
			minS = s.Sig[i]
		}
		if s.Sig[i] > maxS {
			maxS = s.Sig[i]
		}
		if s.Ret[i] < minR {
			minR = s.Ret[i]
		}
		if s.Ret[i] > maxR {
			maxR = s.Ret[i]
		}
	}
	if maxS == minS || maxR == minR {
		return 0, 0
	}

	joint := make([][]float64, sigBins)
	for i := range joint {
		joint[i] = make([]float64, retBins)
	}
	margS := make([]float64, sigBins)
	margR := make([]float64, retBins)

	for i := 0; i < n; i++ {
		sb := binIndex(s.Sig[i], minS, maxS, sigBins)
		rb := binIndex(s.Ret[i], minR, maxR, retBins)
		joint[sb][rb]++
		margS[sb]++
		margR[rb]++
	}

	nf := float64(n)
	for i := 0; i < sigBins; i++ {
		margS[i] /= nf
	}
	for j := 0; j < retBins; j++ {
		margR[j] /= nf
	}
	for i := 0; i < sigBins; i++ {
		for j := 0; j < retBins; j++ {
			joint[i][j] /= nf
		}
	}

	HY := 0.0
	for j := 0; j < retBins; j++ {
		p := margR[j]
		if p > 0 {
			HY -= p * math.Log(p)
		}
	}
	if HY <= 0 {
		return 0, 0
	}

	for i := 0; i < sigBins; i++ {
		for j := 0; j < retBins; j++ {
			pxy := joint[i][j]
			if pxy <= 0 {
				continue
			}
			px := margS[i]
			py := margR[j]
			if px <= 0 || py <= 0 {
				continue
			}
			mi += pxy * math.Log(pxy/(px*py))
		}
	}
	nmi = mi / HY
	return mi, nmi
}

func binIndex(v, minV, maxV float64, bins int) int {
	if v <= minV {
		return 0
	}
	if v >= maxV {
		return bins - 1
	}
	r := (v - minV) / (maxV - minV)
	idx := int(r * float64(bins))
	if idx < 0 {
		idx = 0
	}
	if idx >= bins {
		idx = bins - 1
	}
	return idx
}

// 5. Δ Log-loss (cross-entropy improvement vs baseline)
func (s *ICStats) DeltaLogLoss() (baseline, model, delta float64) {
	n := len(s.Ret)
	if n == 0 || len(s.Sig) != n {
		return 0, 0, 0
	}

	const eps = 1e-9
	labels := make([]int, n) // 0=Down, 1=Flat, 2=Up
	var counts [3]int

	for i, r := range s.Ret {
		var c int
		if r > eps {
			c = 2
		} else if r < -eps {
			c = 0
		} else {
			c = 1
		}
		labels[i] = c
		counts[c]++
	}

	total := float64(n)
	baseProb := [3]float64{}
	for c := 0; c < 3; c++ {
		if counts[c] > 0 {
			baseProb[c] = float64(counts[c]) / total
		} else {
			baseProb[c] = 1e-12
		}
	}

	var baseLoss float64
	for i := 0; i < n; i++ {
		p := baseProb[labels[i]]
		baseLoss -= math.Log(p)
	}
	baseLoss /= total

	const K = 10
	minS, maxS := s.Sig[0], s.Sig[0]
	for i := 1; i < n; i++ {
		if s.Sig[i] < minS {
			minS = s.Sig[i]
		}
		if s.Sig[i] > maxS {
			maxS = s.Sig[i]
		}
	}
	if maxS == minS {
		return baseLoss, baseLoss, 0
	}

	binCounts := make([][3]int, K)
	binTotals := make([]int, K)
	for i := 0; i < n; i++ {
		b := binIndex(s.Sig[i], minS, maxS, K)
		c := labels[i]
		binCounts[b][c]++
		binTotals[b]++
	}

	probs := make([][3]float64, K)
	for b := 0; b < K; b++ {
		if binTotals[b] == 0 {
			probs[b] = baseProb
			continue
		}
		denom := float64(binTotals[b]) + 3.0
		for c := 0; c < 3; c++ {
			probs[b][c] = (float64(binCounts[b][c]) + 1.0) / denom
		}
	}

	var modelLoss float64
	for i := 0; i < n; i++ {
		b := binIndex(s.Sig[i], minS, maxS, K)
		c := labels[i]
		p := probs[b][c]
		modelLoss -= math.Log(p)
	}
	modelLoss /= total

	return baseLoss, modelLoss, baseLoss - modelLoss
}

// ============================================================================
//  SYMBOL REPORT / PORTFOLIO AGGREGATION
// ============================================================================

type SymbolReport struct {
	Symbol string
	Lock   sync.Mutex

	Signals map[SignalID]*[HzCount]ICStats
	Trades  map[SignalID]*[HzCount]AdvancedStats
}

func NewSymbolReport(sym string) *SymbolReport {
	return &SymbolReport{
		Symbol:  sym,
		Signals: make(map[SignalID]*[HzCount]ICStats),
		Trades:  make(map[SignalID]*[HzCount]AdvancedStats),
	}
}

type Portfolio struct {
	Assets map[string]*SymbolReport
	Mu     sync.Mutex
}

func (p *Portfolio) MergeLocal(local *SymbolReport) {
	p.Mu.Lock()
	global, ok := p.Assets[local.Symbol]
	if !ok {
		global = NewSymbolReport(local.Symbol)
		p.Assets[local.Symbol] = global
	}
	p.Mu.Unlock()

	global.Lock.Lock()
	defer global.Lock.Unlock()

	for k, v := range local.Signals {
		if _, ok := global.Signals[k]; !ok {
			global.Signals[k] = &[HzCount]ICStats{}
		}
		for h := 0; h < int(HzCount); h++ {
			dst := &global.Signals[k][h]
			src := &v[h]
			// Bound by maxICSamples already on insertion.
			dst.Sig = append(dst.Sig, src.Sig...)
			dst.Ret = append(dst.Ret, src.Ret...)
			if len(dst.Sig) > maxICSamples {
				dst.Sig = dst.Sig[:maxICSamples]
				dst.Ret = dst.Ret[:maxICSamples]
			}
		}
	}
	for k, v := range local.Trades {
		if _, ok := global.Trades[k]; !ok {
			global.Trades[k] = &[HzCount]AdvancedStats{}
		}
		for h := 0; h < int(HzCount); h++ {
			d := &global.Trades[k][h]
			s := &v[h]

			d.Count += s.Count
			d.Wins += s.Wins
			d.TotalPnL += s.TotalPnL
			d.TotalFees += s.TotalFees
			d.PnL_Markout += s.PnL_Markout
			d.PnL_Real += s.PnL_Real
			d.SumPnL += s.SumPnL
			d.SumPnL2 += s.SumPnL2
			d.SumPnL3 += s.SumPnL3
			d.SumPnL4 += s.SumPnL4

			// Append but cap at maxReturnsPerStat
			space := maxReturnsPerStat - len(d.Returns)
			if space > 0 {
				if len(s.Returns) < space {
					space = len(s.Returns)
				}
				d.Returns = append(d.Returns, s.Returns[:space]...)
			}

			if s.MaxDD > d.MaxDD {
				d.MaxDD = s.MaxDD
			}
			if s.PeakPnL > d.PeakPnL {
				d.PeakPnL = s.PeakPnL
			}
		}
	}
}

// ============================================================================
//  CORE STRATEGY LOOP: TBBO → Signals → Metrics (no execution sim)
// ============================================================================

func RunStrategy(raw *TBBOColumns, config AssetConfig, report *SymbolReport) {
	n := raw.Count
	if n < 2000 {
		return
	}

	// --- BCE HOISTING: verify column lengths once ---
	if len(raw.Prices) < n || len(raw.BidPx) < n || len(raw.AskPx) < n ||
		len(raw.BidSz) < n || len(raw.AskSz) < n || len(raw.TsEvent) < n {
		panic("corrupt TBBO column length")
	}

	// Hoist slice headers to locals (helps BCE and register allocation)
	tsEvents := raw.TsEvent[:n]
	prices := raw.Prices[:n]
	bidPxs := raw.BidPx[:n]
	askPxs := raw.AskPx[:n]
	bidSzs := raw.BidSz[:n]
	askSzs := raw.AskSz[:n]

	mp := NewMarketPhysics()
	signals := &SignalEngine{}

	// --- INIT REPORTING POINTERS ---
	var sigStats [NumSignals][HzCount]*ICStats
	var trdStats [NumSignals][HzCount]*AdvancedStats

	report.Lock.Lock()
	for i, id := range ActiveSignals {
		if _, ok := report.Signals[id]; !ok {
			report.Signals[id] = &[HzCount]ICStats{}
			report.Trades[id] = &[HzCount]AdvancedStats{}
		}
		for h := 0; h < int(HzCount); h++ {
			sigStats[i][h] = &report.Signals[id][h]
			trdStats[i][h] = &report.Trades[id][h]
		}
	}
	report.Lock.Unlock()

	cursors := [HzCount]int{}

	// Initialize physics state with first tick
	mp.PrevTime = tsEvents[0]
	mp.PrevPrice = prices[0]
	mp.PrevMid = (bidPxs[0] + askPxs[0]) * 0.5
	mp.PrevBidSz = bidSzs[0]
	mp.PrevAskSz = askSzs[0]

	var atoms Atoms
	var alphas [NumSignals]float64

	for i := 1; i < n; i++ {
		tNow := tsEvents[i]

		// Pre-compute cursors for horizons (amortized O(1))
		for h := 0; h < int(HzCount); h++ {
			c := cursors[h]
			if c < i {
				c = i
			}
			tgt := tNow + HorizonDurations[h]
			for c < n && tsEvents[c] < tgt {
				c++
			}
			if c >= n {
				c = n - 1
			}
			cursors[h] = c
		}

		// Update microstructure atoms and signals
		mp.UpdateAtoms(&atoms, i, raw)
		signals.Compute(&atoms, mp, raw, i, &alphas)

		// For each horizon, record:
		// - signal vs future log-return (IC, MI, ΔLL)
		// - simple directional strategy returns: sign(signal) * retLog
		for h := 0; h < int(HzCount); h++ {
			c := cursors[h]
			futMid := (bidPxs[c] + askPxs[c]) * 0.5
			retLog := math.Log(futMid / atoms.MidPrice)

			for sIdx := 0; sIdx < NumSignals; sIdx++ {
				sig := alphas[sIdx]
				sigStats[sIdx][h].Observe(sig, retLog)

				if sig == 0 || math.IsNaN(sig) {
					continue
				}
				dir := 1.0
				if sig < 0 {
					dir = -1.0
				}
				stratRet := dir * retLog
				trdStats[sIdx][h].Update(stratRet, stratRet, 0.0)
			}
		}

		mp.UpdateState(i, raw, &atoms)
	}
}
