package main

import (
	"math"
	"sync"
)

// Global Stats Container
type StudyAggregator struct {
	Stats [AtomCount][HzCount][2]RobustStats
}

var StudyPool = sync.Pool{
	New: func() any { return &StudyAggregator{} },
}

func RunMultiHorizonStudy(raw *TBBOColumns, agg *StudyAggregator) {
	n := raw.Count
	if n < 1000 {
		return
	}

	// 1. Setup Pointers
	ts := raw.TsEvent
	prices := raw.Prices
	bp := raw.BidPx
	ap := raw.AskPx
	bs := raw.BidSz
	as := raw.AskSz
	bc := raw.BidCt
	ac := raw.AskCt
	sides := raw.Sides
	sizes := raw.Sizes

	splitIdx := int(float64(n) * (1.0 - OOSRatio))

	// 2. Horizon Cursors
	cursors := [HzCount]int{}

	// 3. State Variables
	var prevP float64 = prices[0]
	var prevT uint64 = ts[0]
	const epsilon = 1e-9

	for i := 0; i < n; i++ {
		curT := ts[i]
		curP := prices[i]

		// Robust Mid
		mid := (bp[i] + ap[i]) * 0.5
		if mid < epsilon {
			mid = curP
		}

		// IS/OOS Bucket
		bucket := 0
		if i >= splitIdx {
			bucket = 1
		}

		// --- A. CURSOR CHASE (Targets) ---
		var returns [HzCount]float64
		for h := 0; h < int(HzCount); h++ {
			targetTime := curT + HorizonDurations[h]
			c := cursors[h]
			if c < i {
				c = i
			}
			for c < n && ts[c] < targetTime {
				c++
			}
			cursors[h] = c

			if c >= n {
				returns[h] = math.NaN()
			} else {
				futMid := (raw.AskPx[c] + raw.BidPx[c]) * 0.5
				if futMid < epsilon {
					futMid = raw.Prices[c]
				}

				if mid > epsilon && futMid > epsilon {
					returns[h] = math.Log(futMid / mid)
				} else {
					returns[h] = math.NaN()
				}
			}
		}

		// --- B. BETTER MATH KERNELS ---

		curQ := float64(sizes[i])
		curS := float64(sides[i])
		curBS := float64(bs[i])
		curAS := float64(as[i])

		dt := curT - prevT
		if dt == 0 {
			dt = 1
		}

		update := func(atom AtomID, signal float64) {
			for h := 0; h < int(HzCount); h++ {
				agg.Stats[atom][h][bucket].Update(signal, returns[h])
			}
		}

		// 1. Trade Sign (Baseline)
		update(AtomTradeSign, curS)

		// 2. Signed Volume (Log-Damped)
		// Large trades matter, but 10x size != 10x signal. Log dampens tails.
		logVol := math.Log(1.0 + curQ)
		update(AtomSignedVol, curS*logVol)

		// 3. Log-Space Volume Imbalance (Robust)
		// Reduces sensitivity to "whale" spoofing.
		lnB := math.Log(1.0 + curBS)
		lnA := math.Log(1.0 + curAS)
		logImbal := (lnB - lnA) / (lnB + lnA + epsilon)
		update(AtomVolImbalance, logImbal)

		// 4. Log-Time Velocity
		// HFT time deltas are power-law distributed. Linear dt is too noisy.
		// We use log(1 + dt_nanos) as the denominator.
		// We also scale by LogVolume.
		lnDt := math.Log(math.E + float64(dt)) // Base e offset
		logVel := (curS * logVol) / lnDt
		update(AtomSignedVelocity, logVel)

		// 5. Aggressor Deviation (Replaces Effective Spread)
		// "How aggressively did they pay?"
		// (Price - Mid) * Side.
		// If Buy(1) pays > Mid, result is Positive (Bullish pressure).
		// If Sell(-1) pays < Mid, result is Positive (Bearish pressure? No.)
		// Wait: Sell < Mid means they crossed spread aggressively.
		// We want: Aggressive Buy -> +Signal. Aggressive Sell -> -Signal.
		// (Price - Mid) is positive for Aggro Buy.
		// (Price - Mid) is negative for Aggro Sell.
		// So we just use (Price - Mid). No Side multiplication needed?
		// No, usually signed. Let's normalize by spread.
		spread := ap[i] - bp[i]
		if spread < epsilon {
			spread = epsilon
		}
		aggDev := (curP - mid) / spread
		// If Price > Mid (Aggro Buy), aggDev > 0.
		// If Price < Mid (Aggro Sell), aggDev < 0.
		update(AtomEffectiveSpread, aggDev)

		// 6. MicroPrice Deviation (FIXED SIGN)
		// Micro = Weighted Mid.
		// If Micro > Mid, the book is weighted to Bids -> Bullish.
		// Signal = Micro - Mid.
		micro := (bp[i]*curAS + ap[i]*curBS) / (curAS + curBS + epsilon)
		microDev := (micro - mid) * 10000.0 // Scale up small numbers
		update(AtomMicroDev, microDev)

		// 7. Count Imbalance (Log-Space)
		// Counts are robust, but let's log them too to match VolImbalance logic.
		lnBC := math.Log(1.0 + float64(bc[i]))
		lnAC := math.Log(1.0 + float64(ac[i]))
		ctImbal := (lnBC - lnAC) / (lnBC + lnAC + epsilon)
		update(AtomCountImbalance, ctImbal)

		// 8. Instant Amihud (Impact / LogVol)
		absRet := math.Abs(curP - prevP)
		amihud := absRet / (logVol + 1.0)
		update(AtomInstantAmihud, amihud)

		prevP = curP
		prevT = curT
	}
}
