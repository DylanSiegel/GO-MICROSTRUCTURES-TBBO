package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"
)

const GapThreshold = 1 * time.Second

func runCheck() {
	fmt.Println(">>> DATA FORENSICS: QuantDev Binary Check (GNC3) <<<")

	files, _ := filepath.Glob("*.quantdev")
	if len(files) == 0 {
		fmt.Println("No .quantdev files found.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "FILE\tTICKS\tGAP(>1s)\tMAX_GAP\tBAD_PX\tSTATUS")
	fmt.Fprintln(w, "----\t-----\t--------\t-------\t------\t------")

	for _, path := range files {
		checkBinaryFile(path, w)
	}
	w.Flush()
}

func checkBinaryFile(path string, w *tabwriter.Writer) {
	cols, err := LoadQuantDev(path)
	if err != nil {
		fmt.Fprintf(w, "%s\tERR\t-\t-\t-\t%v\n", filepath.Base(path), err)
		return
	}
	defer TBBOPool.Put(cols)

	n := cols.Count
	if n == 0 {
		fmt.Fprintf(w, "%s\t0\t-\t-\t-\tEMPTY\n", filepath.Base(path))
		return
	}

	gaps := 0
	maxGap := time.Duration(0)
	badPx := 0

	times := cols.TsEvent
	prices := cols.Prices

	for i := 1; i < n; i++ {
		dt := times[i] - times[i-1]
		dur := time.Duration(dt) * time.Nanosecond
		if dur > GapThreshold {
			gaps++
		}
		if dur > maxGap {
			maxGap = dur
		}
		if prices[i] <= 0.0001 {
			badPx++
		}
	}

	status := "OK"
	if gaps > 0 || badPx > 0 {
		status = "WARN"
	}
	fmt.Fprintf(w, "%s\t%d\t%d\t%s\t%d\t%s\n",
		filepath.Base(path), n, gaps, maxGap.Round(time.Millisecond), badPx, status)
}
