package main

import (
	"fmt"
	"os"
	"runtime"
	"time"
)

func main() {
	// Use all logical CPUs for scheduling; per-command concurrency is limited separately.
	runtime.GOMAXPROCS(runtime.NumCPU())

	if len(os.Args) < 2 {
		printHelp()
		os.Exit(1)
	}

	cmd := os.Args[1]
	start := time.Now()

	switch cmd {
	case "data":
		// Ingests raw .dbn files into the high-performance .quantdev format
		runData()
	case "test":
		// Runs the Microstructure Backtest + Metrics
		runTest()
	case "check":
		// Forensic analysis of data quality
		runCheck()
	default:
		printHelp()
	}
	fmt.Printf("\n[sys] Time: %s\n", time.Since(start))
}

func printHelp() {
	fmt.Println("Usage: go run . [data|test|check]")
	fmt.Println("  data  -> Convert raw Databento (.dbn) to optimized format")
	fmt.Println("  test  -> Run strategy + metrics")
	fmt.Println("  check -> Analyze data files for gaps and packet loss")
}
