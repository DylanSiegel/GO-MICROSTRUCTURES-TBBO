package main

import (
	"sync"
	"unsafe"
)

const (
	// Compute-heavy parallelism (pure CPU work)
	CPUThreads = 24

	// Disk I/O parallelism (streaming DBN -> quantdev)
	// 4–8 tends to be near-optimal for NVMe; tune if needed.
	IOThreads = 8

	// Memory-heavy backtest parallelism (runTest)
	TestMaxParallel = 4

	PxScale = 1e-9
)

// --- CONFIGURATION ---
const (
	// Physics
	Epsilon     = 1e-9
	MaxLookback = 50

	// Simulation
	BaseLatencyNS = 15_000_000 // 15ms
	MaxJitterNS   = 10_000_000 // 10ms
)

// --- HORIZONS: 10s / 20s / 30s ONLY ---

type HorizonID int

const (
	Hz10s HorizonID = iota
	Hz20s
	Hz30s
	HzCount
)

var HorizonDurations = [HzCount]uint64{
	10_000_000_000, // 10s
	20_000_000_000, // 20s
	30_000_000_000, // 30s
}

var HorizonNames = [HzCount]string{"10s", "20s", "30s"}

// Flags bitfield (matches DBN FlagSet raw bits as of DBN v2)
const (
	LastFlag          = 1 << 0 // "end of event" / record is last for that event
	SnapshotFlag      = 1 << 1
	MbpFlag           = 1 << 2
	TobFlag           = 1 << 3
	PublisherSpecFlag = 1 << 4
	BadTsRecvFlag     = 1 << 5
	MaybeBadBookFlag  = 1 << 6
	// bit 7 currently reserved
)

// --- THE 20 ATOMS (PHYSICS STATE) ---
type Atoms struct {
	// Flow
	SignedVol      float64
	TradeSign      int8
	PriceImpact    float64
	SignedVelocity float64
	WhaleShock     float64
	PressureAlign  float64

	// Friction
	QuotedSpread    float64
	EffectiveSpread float64
	InstantAmihud   float64
	VolImbalance    float64
	CountImbalance  float64

	// Value
	MidPrice   float64
	MicroPrice float64
	MicroDev   float64
	CentMagnet float64
	AvgSzBid   float64
	AvgSzAsk   float64

	// Time / latency
	InterTradeDur uint64
	CaptureLat    int64
	SendDelta     int32

	// Ghost Liquidity
	RealBidSz float64
	RealAskSz float64
}

// --- DATA LAYOUT (Struct of Arrays) ---
// This captures the full TBBO semantics from Databento's MBP-1-on-trade schema.
type TBBOColumns struct {
	Count int

	// Identity / routing
	PublisherID  []uint16
	InstrumentID []uint32

	// Timing
	TsEvent   []uint64
	TsRecv    []uint64
	TsInDelta []int32

	// Event
	Prices    []float64 // trade/update price
	Sizes     []float64 // order quantity
	Sides     []int8    // -1 = Aggressive sell, +1 = Aggressive buy, 0 = None/unknown
	Actions   []int8    // 'T', 'A', 'C', 'M', 'R', 'N', ...
	Flags     []uint8   // DBN FlagSet raw bits
	Depth     []uint8   // TBBO depth field (book level updated)
	Sequences []uint32  // venue message sequence

	// Top of book snapshot (post-event)
	BidPx []float64 // best bid price
	AskPx []float64 // best ask price
	BidSz []float64 // best bid size
	AskSz []float64 // best ask size
	BidCt []uint32  // best bid order count
	AskCt []uint32  // best ask order count
}

func (c *TBBOColumns) Reset() {
	c.Count = 0

	c.PublisherID = c.PublisherID[:0]
	c.InstrumentID = c.InstrumentID[:0]

	c.TsEvent = c.TsEvent[:0]
	c.TsRecv = c.TsRecv[:0]
	c.TsInDelta = c.TsInDelta[:0]

	c.Prices = c.Prices[:0]
	c.Sizes = c.Sizes[:0]
	c.Sides = c.Sides[:0]
	c.Actions = c.Actions[:0]
	c.Flags = c.Flags[:0]
	c.Depth = c.Depth[:0]
	c.Sequences = c.Sequences[:0]

	c.BidPx = c.BidPx[:0]
	c.AskPx = c.AskPx[:0]
	c.BidSz = c.BidSz[:0]
	c.AskSz = c.AskSz[:0]
	c.BidCt = c.BidCt[:0]
	c.AskCt = c.AskCt[:0]
}

// Still useful for non-decoder paths if you ever have them.
func (c *TBBOColumns) EnsureCapacity(n int) {
	if cap(c.TsEvent) < n {
		// Identity
		c.PublisherID = make([]uint16, 0, n)
		c.InstrumentID = make([]uint32, 0, n)

		// Timing
		c.TsEvent = make([]uint64, 0, n)
		c.TsRecv = make([]uint64, 0, n)
		c.TsInDelta = make([]int32, 0, n)

		// Event
		c.Prices = make([]float64, 0, n)
		c.Sizes = make([]float64, 0, n)
		c.Sides = make([]int8, 0, n)
		c.Actions = make([]int8, 0, n)
		c.Flags = make([]uint8, 0, n)
		c.Depth = make([]uint8, 0, n)
		c.Sequences = make([]uint32, 0, n)

		// BBO
		c.BidPx = make([]float64, 0, n)
		c.AskPx = make([]float64, 0, n)
		c.BidSz = make([]float64, 0, n)
		c.AskSz = make([]float64, 0, n)
		c.BidCt = make([]uint32, 0, n)
		c.AskCt = make([]uint32, 0, n)
	}
}

var TBBOPool = sync.Pool{New: func() any { return &TBBOColumns{} }}

// -----------------------------------------------------------------------------
// Shared unsafe helper: convert any slice to []byte without extra alloc.
// Used by encoder.go and decoder.go.
// -----------------------------------------------------------------------------
func asBytes[T any](s []T) []byte {
	if len(s) == 0 {
		return nil
	}
	sizeInBytes := len(s) * int(unsafe.Sizeof(s[0]))
	return unsafe.Slice((*byte)(unsafe.Pointer(&s[0])), sizeInBytes)
}

// -----------------------------------------------------------------------------
// resize: reuse existing backing arrays when possible (critical for sync.Pool).
// -----------------------------------------------------------------------------
func resize[T any](s []T, n int) []T {
	if cap(s) < n {
		return make([]T, n)
	}
	return s[:n]
}
