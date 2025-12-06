package main

import (
	"fmt"
	"os"
	"runtime"
	"time"
)

func main() {
	runtime.GOMAXPROCS(CPUThreads)

	if len(os.Args) < 2 {
		printHelp()
		os.Exit(1)
	}

	cmd := os.Args[1]
	start := time.Now()

	switch cmd {
	case "data":
		runData()
	case "test":
		runTest() // Uses zero-alloc test.go
	case "check":
		runCheck()

	default:
		fmt.Printf("Unknown command: %s\n", cmd)
		printHelp()
		os.Exit(1)
	}

	fmt.Printf("\n[sys] Execution Time: %s\n", time.Since(start))
}

func printHelp() {
	fmt.Println("Usage: go run . [command]")
	fmt.Println("  data   - Ingest .dbn files -> .quantdev (GNC3 format)")
	fmt.Println("  test   - Compute Atoms & Stats (Parallel)")
	fmt.Println("  check  - Data Integrity Scan")
	fmt.Println("  bench  - Hardware Capability Benchmark (Unsafe)")
}
