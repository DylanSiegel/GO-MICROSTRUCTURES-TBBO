package main

import (
	"math"
	"unique"
)

// ============================================================================
//  Signal indices & IDs
// ============================================================================

type SignalID = unique.Handle[string]

const (
	// The 5 Atomic Primitives
	SigIdx_TrueOFI     = iota // Cont et al. Imbalance (Iceberg detection)
	SigIdx_Crowding           // Retail vs Inst skew
	SigIdx_LatUrgency         // Systemic congestion/urgency
	SigIdx_SweepDepth         // Breakout detection (kappa)
	SigIdx_Liquidation        // Forced run detection

	// The Integration Vector (200ms Aggregate)
	SigIdx_IntegratedState

	NumSignals
)

// Human-readable IDs for reporting / metrics.
var (
	Signal_TrueOFI     = unique.Make("Alpha_1_TrueOFI")
	Signal_Crowding    = unique.Make("Alpha_2_CrowdingRatio")
	Signal_LatUrgency  = unique.Make("Alpha_3_LatencyUrgency")
	Signal_SweepDepth  = unique.Make("Alpha_4_SweepPenetration")
	Signal_Liquidation = unique.Make("Alpha_5_LiquidationRun")
	Signal_Integrated  = unique.Make("Alpha_Integrated_StateVector")
)

var ActiveSignals = []SignalID{
	Signal_TrueOFI,
	Signal_Crowding,
	Signal_LatUrgency,
	Signal_SweepDepth,
	Signal_Liquidation,
	Signal_Integrated,
}

// ============================================================================
//  Market Physics (State Machine)
// ============================================================================

type LiquidationState struct {
	Active     bool
	Side       int8    // +1 for Buy run, -1 for Sell run
	StartPrice float64 // price where the run started
	MaxPrice   float64 // for buys: max; for sells: min
	Volume     float64 // accumulated volume in this run
	LastSeq    uint32  // last sequence seen in this run
}

type MarketPhysics struct {
	// Identity & continuity
	LastSeq   uint32
	validHist bool // false if we hit a sequence gap and need to rebuild windows

	// Previous L1 snapshot (t-1)
	PrevTime  uint64
	PrevBidSz float64
	PrevAskSz float64
	PrevBidPx float64
	PrevAskPx float64

	// For metrics.go compatibility
	PrevPrice float64 // last trade price
	PrevMid   float64 // last midprice

	// Rolling integration windows (~200ms layer)
	OFIWindow      *RollingWindow
	AvgBidSzWindow *RollingWindow
	AvgAskSzWindow *RollingWindow
	UrgencyWindow  *RollingWindow
	SweepWindow    *RollingWindow

	// Liquidation state machine
	LiqState LiquidationState
}

func NewMarketPhysics() *MarketPhysics {
	// Windows tuned for ~50–100 events (≈100–500ms in active markets)
	return &MarketPhysics{
		OFIWindow:      NewRollingWindow(64),
		AvgBidSzWindow: NewRollingWindow(128),
		AvgAskSzWindow: NewRollingWindow(128),
		UrgencyWindow:  NewRollingWindow(32),
		SweepWindow:    NewRollingWindow(64),
		validHist:      false,
	}
}

// ============================================================================
//  RollingWindow – exact sliding-window average via ring buffer
// ============================================================================

type RollingWindow struct {
	Buf   []float64
	Head  int
	Sum   float64
	Size  int
	Count int
}

func NewRollingWindow(n int) *RollingWindow {
	if n <= 0 {
		n = 1
	}
	return &RollingWindow{
		Buf:  make([]float64, n),
		Size: n,
	}
}

func (r *RollingWindow) Update(val float64) float64 {
	if r.Size == 0 {
		return val
	}

	// Remove old tail
	r.Sum -= r.Buf[r.Head]

	// Add new head
	r.Buf[r.Head] = val
	r.Sum += val

	r.Head = (r.Head + 1) % r.Size

	if r.Count < r.Size {
		r.Count++
	}

	return r.Sum / float64(r.Count)
}

func (r *RollingWindow) Reset() {
	for i := range r.Buf {
		r.Buf[i] = 0
	}
	r.Sum = 0
	r.Head = 0
	r.Count = 0
}

// ============================================================================
//  Atomic Primitive Calculations
// ============================================================================

const Epsilon = 1e-9

// UpdateAtoms: core physics engine.
// Converts raw TBBO events into the 5 primitives, handling sequence gaps.
func (mp *MarketPhysics) UpdateAtoms(a *Atoms, i int, raw *TBBOColumns) {
	// -------------------------------------------------------------------------
	// 0) Sequence gap detection
	// -------------------------------------------------------------------------
	currentSeq := raw.Sequences[i]
	if mp.validHist && currentSeq != mp.LastSeq+1 {
		// GAP DETECTED: invalidate state to avoid phantom OFI / sweep spikes.
		mp.OFIWindow.Reset()
		mp.AvgBidSzWindow.Reset()
		mp.AvgAskSzWindow.Reset()
		mp.UrgencyWindow.Reset()
		mp.SweepWindow.Reset()
		mp.LiqState = LiquidationState{}
		mp.validHist = false
	}
	mp.LastSeq = currentSeq

	// Current TBBO state
	q_n := raw.Sizes[i]
	p_n := raw.Prices[i]
	s_n := raw.Sides[i] // +1=Buy, -1=Sell, 0=none

	curBidPx := raw.BidPx[i]
	curAskPx := raw.AskPx[i]
	curBidSz := raw.BidSz[i]
	curAskSz := raw.AskSz[i]
	curBidCt := float64(raw.BidCt[i])
	curAskCt := float64(raw.AskCt[i])

	mid := (curBidPx + curAskPx) * 0.5
	a.MidPrice = mid

	// First valid tick (or first after a gap): snapshot and bail.
	if !mp.validHist {
		mp.PrevBidSz = curBidSz
		mp.PrevAskSz = curAskSz
		mp.PrevBidPx = curBidPx
		mp.PrevAskPx = curAskPx
		mp.PrevTime = raw.TsEvent[i]
		mp.PrevMid = mid
		// PrevPrice is set on first trade; leave as 0 for now.

		a.RawOFI = 0
		a.CrowdSkew = 0
		a.LatUrgency = 0
		a.SweepKappa = 0
		a.LiqStrength = 0

		mp.validHist = true
		return
	}

	// =====================================================================
	// 1) True Order Flow Imbalance (OFI)   OFI = q * I - ΔQ_passive
	// =====================================================================
	ofiVal := 0.0

	if raw.Actions[i] == 'T' { // trade event
		if s_n == 1 {
			// Buy hits Ask: passive side is Ask
			deltaAsk := curAskSz - mp.PrevAskSz
			// Expected deltaAsk ≈ -q_n. Replenishment => deltaAsk > -q_n.
			ofiVal = q_n + deltaAsk // q*I - ΔQ, I = +1
		} else if s_n == -1 {
			// Sell hits Bid: passive side is Bid
			deltaBid := curBidSz - mp.PrevBidSz
			// Expected deltaBid ≈ -q_n. Replenishment => deltaBid > -q_n.
			ofiVal = -q_n - deltaBid // q*I - ΔQ, I = -1
		}
	} else {
		// Pure book updates: treat relative change in L1 as passive imbalance.
		deltaBid := curBidSz - mp.PrevBidSz
		deltaAsk := curAskSz - mp.PrevAskSz
		ofiVal = deltaBid - deltaAsk
	}

	a.RawOFI = mp.OFIWindow.Update(ofiVal)

	// =====================================================================
	// 2) Crowding Ratio (Retail vs Inst)
	//     Z_crowd = E[BidSz/BidCt] - E[AskSz/AskCt]
	// =====================================================================
	avgBidOrder := 0.0
	if curBidCt > 0 {
		avgBidOrder = curBidSz / curBidCt
	}
	avgAskOrder := 0.0
	if curAskCt > 0 {
		avgAskOrder = curAskSz / curAskCt
	}

	sBid := mp.AvgBidSzWindow.Update(avgBidOrder)
	sAsk := mp.AvgAskSzWindow.Update(avgAskOrder)
	a.CrowdSkew = sBid - sAsk

	// =====================================================================
	// 3) Latency-Adjusted Urgency   U = size / log(1 + delta)
	// =====================================================================
	urgency := 0.0
	if raw.Actions[i] == 'T' && s_n != 0 && q_n > 0 {
		d := float64(raw.TsInDelta[i])
		if d < 0 {
			d = 0
		}

		denom := math.Log1p(d) // safe for d ~ 0 .. large
		if denom < 1.0 {
			denom = 1.0
		}
		urgency = (q_n / denom) * float64(s_n)
	}

	a.LatUrgency = mp.UrgencyWindow.Update(urgency)

	// =====================================================================
	// 4) Sweep Penetration Depth   κ = size / prev_contra_size, only κ ≥ 1
	// =====================================================================
	kappa := 0.0
	if raw.Actions[i] == 'T' && s_n != 0 && q_n > 0 {
		if s_n == 1 {
			// Buy hits Ask; compare to previous Ask size
			if mp.PrevAskSz > Epsilon {
				rawKappa := q_n / mp.PrevAskSz
				if rawKappa >= 1.0 {
					kappa = rawKappa // positive for buys
				}
			}
		} else if s_n == -1 {
			// Sell hits Bid; compare to previous Bid size
			if mp.PrevBidSz > Epsilon {
				rawKappa := q_n / mp.PrevBidSz
				if rawKappa >= 1.0 {
					kappa = -rawKappa // negative for sells
				}
			}
		}
	}

	a.SweepKappa = mp.SweepWindow.Update(kappa)

	// =====================================================================
	// 5) Liquidation / Forced Run Detection
	// =====================================================================
	if raw.Actions[i] == 'T' && s_n != 0 && q_n > 0 {
		// Dataset-specific liquidation/last flags; using bit 128 as in your text.
		isLiquidationFlag := (raw.Flags[i] & 128) != 0

		resetRun := false
		if mp.LiqState.Active {
			if s_n != mp.LiqState.Side {
				// Aggressor side flipped
				resetRun = true
			} else {
				// Check for retracement: price move against the run
				if s_n == 1 && p_n < mp.LiqState.MaxPrice {
					resetRun = true
				}
				if s_n == -1 && p_n > mp.LiqState.MaxPrice {
					resetRun = true
				}
			}
		}

		if resetRun {
			mp.LiqState = LiquidationState{}
		}

		if !mp.LiqState.Active {
			// Start new run
			mp.LiqState.Active = true
			mp.LiqState.Side = s_n
			mp.LiqState.StartPrice = p_n
			mp.LiqState.MaxPrice = p_n
			mp.LiqState.Volume = q_n
			mp.LiqState.LastSeq = currentSeq
		} else {
			// Continue run
			mp.LiqState.Volume += q_n
			if s_n == 1 && p_n > mp.LiqState.MaxPrice {
				mp.LiqState.MaxPrice = p_n
			}
			if s_n == -1 && p_n < mp.LiqState.MaxPrice {
				mp.LiqState.MaxPrice = p_n
			}
			mp.LiqState.LastSeq = currentSeq
		}

		priceDev := math.Abs(p_n - mp.LiqState.StartPrice)
		strength := mp.LiqState.Volume * (1.0 + priceDev*100.0)
		if isLiquidationFlag {
			strength *= 2.0
		}
		a.LiqStrength = strength * float64(mp.LiqState.Side)
	} else {
		// On non-trade events, gently decay the liquidation signal.
		a.LiqStrength *= 0.95
	}

	// ---------------------------------------------------------------------
	// Update internal state for next tick
	// ---------------------------------------------------------------------
	mp.PrevTime = raw.TsEvent[i]
	mp.PrevBidSz = curBidSz
	mp.PrevAskSz = curAskSz
	mp.PrevBidPx = curBidPx
	mp.PrevAskPx = curAskPx

	// Mid always tracks latest mid
	mp.PrevMid = mid

	// PrevPrice updates only on trades with a valid price
	if raw.Actions[i] == 'T' && p_n > 0 {
		mp.PrevPrice = p_n
	}
}

// ============================================================================
//  Signal Engine: Output Generation
// ============================================================================

type SignalEngine struct{}

// Compute populates the signal vector based on the Atoms
func (se *SignalEngine) Compute(
	atoms *Atoms,
	mp *MarketPhysics,
	raw *TBBOColumns,
	i int,
	out *[NumSignals]float64,
) {
	// Zero out
	for k := 0; k < NumSignals; k++ {
		out[k] = 0
	}

	// 1) True OFI – iceberg / cancel-adjusted flow
	out[SigIdx_TrueOFI] = clampFloat64(atoms.RawOFI*0.5, -5.0, 5.0)

	// 2) Crowding Ratio – Inst vs Retail skew
	out[SigIdx_Crowding] = clampFloat64(atoms.CrowdSkew*0.2, -5.0, 5.0)

	// 3) Latency Urgency
	out[SigIdx_LatUrgency] = clampFloat64(atoms.LatUrgency*2.0, -5.0, 5.0)

	// 4) Sweep Penetration (κ ≥ 1)
	out[SigIdx_SweepDepth] = clampFloat64(atoms.SweepKappa*2.0, -5.0, 5.0)

	// 5) Liquidation Run
	liqSig := 0.0
	if math.Abs(atoms.LiqStrength) > 50.0 { // tune per asset
		liqSig = atoms.LiqStrength * 0.01
	}
	out[SigIdx_Liquidation] = clampFloat64(liqSig, -5.0, 5.0)

	// 6) Integrated State Vector (200ms prediction layer)
	vectorSum :=
		1.5*out[SigIdx_TrueOFI] +
			1.2*out[SigIdx_SweepDepth] +
			0.8*out[SigIdx_Crowding] +
			0.5*out[SigIdx_LatUrgency] +
			1.0*out[SigIdx_Liquidation]

	out[SigIdx_IntegratedState] = clampFloat64(vectorSum, -10.0, 10.0)
}

// ============================================================================
//  Helpers
// ============================================================================

func clampFloat64(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}
