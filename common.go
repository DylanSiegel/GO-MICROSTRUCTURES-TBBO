package main

import (
	"sync"
)

// Global configuration
const (
	CPUThreads = 24 // Zen 4 (12C/24T)
	BaseDir    = "data"
	PxScale    = 1e-9 // Raw Int64 -> Float conversion
)

// --- HORIZON CONFIGURATION ---
type HorizonID int

const (
	Hz10s HorizonID = iota
	Hz30s
	Hz1m
	Hz5m
	HzCount
)

// Time deltas in Nanoseconds
var HorizonDurations = [HzCount]uint64{
	10_000_000_000,  // 10s
	30_000_000_000,  // 30s
	60_000_000_000,  // 1m
	300_000_000_000, // 5m
}

var HorizonNames = [HzCount]string{"10s", "30s", "1m ", "5m "}

// Validation Config
const OOSRatio = 0.3 // Last 30% of rows are Out-of-Sample

// --- ATOM INDEXING ---
type AtomID int

const (
	AtomSignedVol AtomID = iota
	AtomTradeSign
	AtomPriceImpact
	AtomSignedVelocity
	AtomWhaleShock
	AtomPressureAlign
	AtomQuotedSpread
	AtomEffectiveSpread
	AtomInstantAmihud
	AtomVolImbalance
	AtomCountImbalance
	AtomMidPrice
	AtomMicroPrice
	AtomMicroDev
	AtomCentMagnet
	AtomAvgSzBid
	AtomAvgSzAsk
	AtomInterTradeDur
	AtomCaptureLat
	AtomSendDelta
	AtomCount
)

var AtomNames = [AtomCount]string{
	"SignedVol", "TradeSign", "PriceImpact", "SignedVelocity", "WhaleShock", "PressureAlign",
	"QuotedSpread", "EffectiveSpread", "InstantAmihud", "VolImbalance", "CountImbalance",
	"MidPrice", "MicroPrice", "MicroDev", "CentMagnet", "AvgSzBid", "AvgSzAsk",
	"InterTradeDur", "CaptureLat", "SendDelta",
}

// --- DATA LAYOUT (SoA) ---
type TBBOColumns struct {
	Count int

	TsEvent   []uint64
	TsRecv    []uint64
	TsInDelta []int32

	Prices []float64
	Sizes  []uint32
	Sides  []int8
	Flags  []uint8

	BidPx []float64
	AskPx []float64
	BidSz []uint32
	AskSz []uint32
	BidCt []uint32
	AskCt []uint32
}

func (c *TBBOColumns) Reset() {
	c.Count = 0
	c.TsEvent = c.TsEvent[:0]
	c.TsRecv = c.TsRecv[:0]
	c.TsInDelta = c.TsInDelta[:0]
	c.Prices = c.Prices[:0]
	c.Sizes = c.Sizes[:0]
	c.Sides = c.Sides[:0]
	c.Flags = c.Flags[:0]
	c.BidPx = c.BidPx[:0]
	c.AskPx = c.AskPx[:0]
	c.BidSz = c.BidSz[:0]
	c.AskSz = c.AskSz[:0]
	c.BidCt = c.BidCt[:0]
	c.AskCt = c.AskCt[:0]
}

func (c *TBBOColumns) EnsureCapacity(n int) {
	if cap(c.Prices) >= n {
		return
	}
	// Helper to reduce repetition
	growF64 := func() []float64 { return make([]float64, 0, n) }
	growU64 := func() []uint64 { return make([]uint64, 0, n) }
	growU32 := func() []uint32 { return make([]uint32, 0, n) }
	growI32 := func() []int32 { return make([]int32, 0, n) }
	growI8 := func() []int8 { return make([]int8, 0, n) }
	growU8 := func() []uint8 { return make([]uint8, 0, n) }

	c.TsEvent = growU64()
	c.TsRecv = growU64()
	c.TsInDelta = growI32()
	c.Prices = growF64()
	c.Sizes = growU32()
	c.Sides = growI8()
	c.Flags = growU8()
	c.BidPx = growF64()
	c.AskPx = growF64()
	c.BidSz = growU32()
	c.AskSz = growU32()
	c.BidCt = growU32()
	c.AskCt = growU32()
}

var TBBOPool = sync.Pool{
	New: func() any {
		const cap = 1_000_000
		return &TBBOColumns{
			TsEvent:   make([]uint64, 0, cap),
			TsRecv:    make([]uint64, 0, cap),
			TsInDelta: make([]int32, 0, cap),
			Prices:    make([]float64, 0, cap),
			Sizes:     make([]uint32, 0, cap),
			Sides:     make([]int8, 0, cap),
			Flags:     make([]uint8, 0, cap),
			BidPx:     make([]float64, 0, cap),
			AskPx:     make([]float64, 0, cap),
			BidSz:     make([]uint32, 0, cap),
			AskSz:     make([]uint32, 0, cap),
			BidCt:     make([]uint32, 0, cap),
			AskCt:     make([]uint32, 0, cap),
		}
	},
}
