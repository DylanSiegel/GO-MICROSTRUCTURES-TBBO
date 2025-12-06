package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// DBN Constants
const (
	DBNMagic  = "DBN"
	RTypeTBBO = 1 // Record Type 1 is MBP-0/TBBO
)

func runData() {
	fmt.Println(">>> INGESTION: DBN (TBBO) -> QuantDev Binary <<<")

	files, _ := filepath.Glob("*.dbn")
	if len(files) == 0 {
		fmt.Println("[warn] No .dbn files found.")
		return
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, CPUThreads)

	for _, f := range files {
		wg.Add(1)
		sem <- struct{}{}
		go func(path string) {
			defer wg.Done()
			defer func() { <-sem }()
			convertDBNToQuantDev(path)
		}(f)
	}
	wg.Wait()
}

func convertDBNToQuantDev(path string) {
	f, err := os.Open(path)
	if err != nil {
		fmt.Printf("Err %s: %v\n", path, err)
		return
	}
	defer f.Close()

	outPath := strings.TrimSuffix(path, filepath.Ext(path)) + ".quantdev"
	fmt.Printf(" -> Converting %s to %s... ", filepath.Base(path), filepath.Base(outPath))

	enc, err := NewEncoder(outPath)
	if err != nil {
		return
	}
	defer enc.Close()

	// 1. Read Header
	headerBuf := make([]byte, 8)
	startOffset := int64(0)
	if n, _ := f.Read(headerBuf); n == 8 {
		if string(headerBuf[0:3]) == DBNMagic {
			metaLen := binary.LittleEndian.Uint32(headerBuf[4:8])
			startOffset = int64(8 + metaLen)
		}
	}
	f.Seek(startOffset, io.SeekStart)

	// 2. Optimized Streaming Loop
	const ChunkSize = 64 * 1024 // 64KB Buffer
	buf := make([]byte, ChunkSize)
	leftover := make([]byte, 0, 256)
	count := 0

	for {
		n, err := f.Read(buf)
		if n == 0 {
			break
		}

		// Combine leftover + new data
		data := buf[:n]
		if len(leftover) > 0 {
			// This alloc is rare (once per chunk boundary)
			data = append(leftover, buf[:n]...)
			leftover = leftover[:0]
		}

		offset := 0
		lenData := len(data)

		for offset < lenData {
			if lenData-offset < 1 {
				leftover = append(leftover, data[offset:]...)
				break
			}

			// DBN Spec: Byte 0 is length in 4-byte words
			lengthWords := int(data[offset])
			if lengthWords == 0 {
				offset++
				continue
			}
			recSize := lengthWords * 4

			if lenData-offset < recSize {
				leftover = append(leftover, data[offset:]...)
				break
			}

			// HOT PATH: Parse Record without allocations
			// We use direct slice indexing which compiles to MOV on amd64
			rec := data[offset : offset+recSize]
			offset += recSize

			if rec[1] != RTypeTBBO || recSize != 80 {
				continue
			}

			// Field Extraction (Manual LittleEndian)
			// Go 1.25+ compiler inlines these efficiently.
			tsEvent := binary.LittleEndian.Uint64(rec[8:16])
			pRaw := int64(binary.LittleEndian.Uint64(rec[16:24]))
			size := binary.LittleEndian.Uint32(rec[24:28])

			// Side char logic (Fixed QF1003)
			// Uses a switch for cleaner jump table generation
			sideChar := rec[29]
			var s int8
			switch sideChar {
			case 'B':
				s = 1
			case 'A':
				s = -1
			}

			flags := rec[30]
			tsRecv := binary.LittleEndian.Uint64(rec[32:40])
			tsDelta := int32(binary.LittleEndian.Uint32(rec[40:44]))

			// BBO Levels
			bpRaw := int64(binary.LittleEndian.Uint64(rec[48:56]))
			apRaw := int64(binary.LittleEndian.Uint64(rec[56:64]))
			bs := binary.LittleEndian.Uint32(rec[64:68])
			as := binary.LittleEndian.Uint32(rec[68:72])
			bc := binary.LittleEndian.Uint32(rec[72:76])
			ac := binary.LittleEndian.Uint32(rec[76:80])

			// Filter Null Prices (DBN null is MaxInt64)
			if pRaw == 9223372036854775807 {
				continue
			}

			enc.AddRow(tsEvent, tsRecv, tsDelta, pRaw, size, s, flags, bpRaw, apRaw, bs, as, bc, ac)
			count++
		}

		if err == io.EOF {
			break
		}
	}

	fmt.Printf("Done (%d rows)\n", count)
}
