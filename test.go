package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"text/tabwriter"
	"time"
)

func runTest() {
	start := time.Now()
	fmt.Println(">>> ALPHA ITERATION: ROBUST LOG-MATH & FIXED SIGNS <<<")
	fmt.Printf("[config] Horizons: %v | OOS Split: Last %.0f%%\n", HorizonNames, OOSRatio*100)

	files, _ := filepath.Glob("*.quantdev")
	if len(files) == 0 {
		fmt.Println("[fatal] No .quantdev files found.")
		return
	}

	master := &StudyAggregator{}
	var mu sync.Mutex

	var wg sync.WaitGroup
	sem := make(chan struct{}, CPUThreads)

	for _, path := range files {
		wg.Add(1)
		sem <- struct{}{}

		go func(p string) {
			defer wg.Done()
			defer func() { <-sem }()

			cols, err := LoadQuantDev(p)
			if err != nil {
				return
			}

			localAgg := StudyPool.Get().(*StudyAggregator)
			*localAgg = StudyAggregator{}

			RunMultiHorizonStudy(cols, localAgg)
			TBBOPool.Put(cols)

			mu.Lock()
			for a := 0; a < int(AtomCount); a++ {
				for h := 0; h < int(HzCount); h++ {
					master.Stats[a][h][0].Merge(localAgg.Stats[a][h][0])
					master.Stats[a][h][1].Merge(localAgg.Stats[a][h][1])
				}
			}
			mu.Unlock()
			StudyPool.Put(localAgg)
			fmt.Print(".")
		}(path)
	}
	wg.Wait()
	fmt.Println("\nDone.")

	printDetailedReport(master, time.Since(start))
}

func printDetailedReport(agg *StudyAggregator, dur time.Duration) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', 0)

	// Custom mapping for our new definitions
	descriptions := map[AtomID]string{
		AtomTradeSign:       "TradeSign (Base)",
		AtomSignedVol:       "SignedLogVol",
		AtomVolImbalance:    "LogVolImbalance",
		AtomCountImbalance:  "LogCountImbalance",
		AtomSignedVelocity:  "LogTimeVelocity",
		AtomEffectiveSpread: "AggressorDev (P-Mid)",
		AtomMicroDev:        "MicroDev (Micro-Mid)",
		AtomInstantAmihud:   "InstantAmihud",
	}

	activeAtoms := []AtomID{
		AtomTradeSign, AtomSignedVol, AtomVolImbalance, AtomCountImbalance,
		AtomSignedVelocity, AtomEffectiveSpread, AtomMicroDev, AtomInstantAmihud,
	}

	for _, atom := range activeAtoms {
		name := descriptions[atom]
		fmt.Fprintf(w, "\n>> %s <<\n", name)
		fmt.Fprintln(w, "HORIZON\tIC(IS)\tIC(OOS)\tOOS_DEC%\tT-STAT\tSHARPE\tSTATUS")
		fmt.Fprintln(w, "-------\t------\t-------\t--------\t------\t------\t------")

		prevIC := 0.0
		monotonic := true

		for h := 0; h < int(HzCount); h++ {
			is := agg.Stats[atom][h][0].Calculate()
			oos := agg.Stats[atom][h][1].Calculate()

			decay := 0.0
			if math.Abs(is.IC) > 1e-5 {
				decay = (1.0 - (oos.IC / is.IC)) * 100
			}

			status := "OK"
			if math.Abs(oos.TStat) < 2.0 {
				status = "NO_SIG"
			} else if decay > 40 {
				status = "DECAY" // >40% drop is concerning
			} else if decay < -40 {
				status = "REGIME" // OOS much stronger
			}

			// Sharpe Proxy (Scaled)
			fmt.Fprintf(w, "%s\t%.4f\t%.4f\t%.1f%%\t%.1f\t%.2f\t%s\n",
				HorizonNames[h], is.IC, oos.IC, decay, oos.TStat, oos.Sharpe, status)

			if h > 0 {
				if (is.IC > 0 && prevIC < 0) || (is.IC < 0 && prevIC > 0) {
					monotonic = false
				}
			}
			prevIC = is.IC
		}

		monoStr := "NO"
		if monotonic {
			monoStr = "YES"
		}
		fmt.Fprintf(w, "Monotonic: %s\n", monoStr)
	}
	w.Flush()
	fmt.Printf("\n[sys] Analysis Time: %s\n", dur)
}
