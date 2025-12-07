package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"
)

const (
	GapThreshold     = 1 * time.Second  // base threshold
	BigIntradayGap   = 60 * time.Second // > 60s within session
	MarketClosureCut = 12 * time.Hour   // anything above is treated as closure
	WarnBigGapFrac   = 0.01             // 1% of ticks have >60s gap → WARN
)

func runCheck() {
	fmt.Println(">>> DATA FORENSICS: QuantDev Binary Check (Smart TBBO) <<<")

	files, _ := filepath.Glob("*.quantdev")
	if len(files) == 0 {
		fmt.Println("No .quantdev files found.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "FILE\tTICKS\tGAP>1s%\tGAP>60s%\tMAX_GAP\tBAD_PX\tSTATUS")
	fmt.Fprintln(w, "----\t-----\t--------\t---------\t-------\t------\t------")

	for _, path := range files {
		checkBinaryFile(path, w)
	}
	w.Flush()
}

func checkBinaryFile(path string, w *tabwriter.Writer) {
	cols, err := LoadQuantDev(path)
	if err != nil {
		fmt.Fprintf(w, "%s\tERR\t-\t-\t-\t-\t%v\n", filepath.Base(path), err)
		return
	}
	defer TBBOPool.Put(cols)

	n := cols.Count
	if n == 0 {
		fmt.Fprintf(w, "%s\t0\t-\t-\t-\t-\tEMPTY\n", filepath.Base(path))
		return
	}

	var (
		gaps1s  int
		gaps60s int
		badPx   int
		maxGap  time.Duration
	)

	times := cols.TsEvent
	prices := cols.Prices
	flags := cols.Flags

	for i := 1; i < n; i++ {
		if flags[i]&BadTsRecvFlag != 0 {
			continue
		}

		dt := times[i] - times[i-1]
		dur := time.Duration(dt) * time.Nanosecond

		if dur > maxGap {
			maxGap = dur
		}

		// Treat very large gaps as market closures – do not count them
		if dur > MarketClosureCut {
			continue
		}

		if dur > GapThreshold {
			gaps1s++
		}
		if dur > BigIntradayGap {
			gaps60s++
		}

		if prices[i] <= 0.0001 {
			badPx++
		}
	}

	frac1s := float64(gaps1s) / float64(n) * 100.0
	frac60s := float64(gaps60s) / float64(n) * 100.0

	status := "OK"
	if badPx > 0 || frac60s > WarnBigGapFrac*100.0 {
		status = "WARN"
	}

	fmt.Fprintf(
		w,
		"%s\t%d\t%.3f\t%.3f\t%s\t%d\t%s\n",
		filepath.Base(path),
		n,
		frac1s,
		frac60s,
		maxGap.Round(time.Millisecond),
		badPx,
		status,
	)
}
