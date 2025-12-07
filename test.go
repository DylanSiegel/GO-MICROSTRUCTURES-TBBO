package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
)

func runTest() {
	start := time.Now()
	fmt.Println(">>> MICROSTRUCTURE SIGNAL PERFORMANCE (PURE ALPHA MODE) <<<")

	files, _ := filepath.Glob("*.quantdev")
	if len(files) == 0 {
		fmt.Println("No .quantdev files found.")
		return
	}

	portfolio := &Portfolio{Assets: make(map[string]*SymbolReport)}

	// Sort files by size (largest first)
	type job struct {
		path string
		size int64
	}
	var jobs []job
	for _, f := range files {
		info, err := os.Stat(f)
		if err == nil {
			jobs = append(jobs, job{path: f, size: info.Size()})
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].size > jobs[j].size })

	var wg sync.WaitGroup
	// Use a smaller concurrency limit for memory-heavy backtest.
	sem := make(chan struct{}, TestMaxParallel)

	for _, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(path string) {
			defer wg.Done()
			defer func() { <-sem }()

			base := filepath.Base(path)
			parts := strings.Split(base, "_")
			sym := "UNKNOWN"
			if len(parts) > 0 {
				sym = strings.ToUpper(strings.TrimSuffix(parts[0], ".quantdev"))
			}

			config := GetAssetConfig(sym)
			cols, err := LoadQuantDev(path)
			if err != nil {
				fmt.Printf("\n[err] %s: %v\n", path, err)
				return
			}
			defer TBBOPool.Put(cols)

			local := NewSymbolReport(sym)
			RunStrategy(cols, config, local)
			portfolio.MergeLocal(local)
			fmt.Print(".")
		}(j.path)
	}
	wg.Wait()

	fmt.Print("\n\n")

	printPortfolio(portfolio)
	fmt.Printf("[sys] Execution Time: %s\n", time.Since(start))
}

func printPortfolio(p *Portfolio) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', 0)

	var syms []string
	for k := range p.Assets {
		syms = append(syms, k)
	}
	sort.Strings(syms)

	for _, sym := range syms {
		r := p.Assets[sym]
		fmt.Fprintf(w, "\n===========================================================================================================\n")
		fmt.Fprintf(w, " ASSET: %s\n", sym)
		fmt.Fprintf(w, "===========================================================================================================\n")

		// SignalID slice, sorted by underlying name
		var sigs []SignalID
		for k := range r.Signals {
			sigs = append(sigs, k)
		}
		sort.Slice(sigs, func(i, j int) bool {
			return sigs[i].Value() < sigs[j].Value()
		})

		for _, sID := range sigs {
			fmt.Fprintf(w, "\n>> %s <<\n", sID.Value())

			// Header for core checklist metrics per horizon
			fmt.Fprintln(w, "HZ\tTRADES\tIC\tRANK_IC\tHIT%\tMI\tNMI\tSHARPE\tWIN%\tW/L\tSKEW\tMAX_DD\tP05\tP01\tΔLOGLOSS\tMARKOUT\tNET_PNL\tAVG_NET")
			fmt.Fprintln(w, "--\t------\t--\t-------\t----\t--\t---\t------\t----\t---\t----\t------\t---\t---\t--------\t-------\t-------\t-------")

			for h := 0; h < int(HzCount); h++ {
				ts := r.Trades[sID][h]
				ss := r.Signals[sID][h]
				if ts.Count == 0 || ss.Count() == 0 {
					continue
				}

				ic := ss.PearsonIC()
				rankIC := ss.RankIC()
				hitRate := ss.HitRate() * 100.0
				mi, nmi := ss.MutualInformation(10, 3)
				baseLL, modelLL, dLL := ss.DeltaLogLoss()

				sharpe := ts.Sharpe()
				winRate := ts.WinRate()
				wl := ts.WinLossRatio()
				skew := ts.Skewness()
				maxDD := ts.MaxDD
				p05 := ts.TailPercentile(0.05)
				p01 := ts.TailPercentile(0.01)
				avgNet := ts.TotalPnL / float64(ts.Count)

				fmt.Fprintf(
					w,
					"%s\t%d\t%.3f\t%.3f\t%.1f\t%.3f\t%.3f\t%.2f\t%.1f\t%.2f\t%.2f\t%.0f\t%.1f\t%.1f\t%.4f/%.4f/%.4f\t%.0f\t%.0f\t%.2f\n",
					HorizonNames[h],
					ts.Count,
					ic,
					rankIC,
					hitRate,
					mi,
					nmi,
					sharpe,
					winRate,
					wl,
					skew,
					maxDD,
					p05,
					p01,
					baseLL, modelLL, dLL,
					ts.PnL_Markout,
					ts.TotalPnL,
					avgNet,
				)
			}
		}
	}
	w.Flush()
}
