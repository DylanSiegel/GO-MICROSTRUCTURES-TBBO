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

const (
	DBNMagic  = "DBN"
	RTypeTBBO = 1 // TBBO is MBP-1-on-trade in Databento's schema; rtype==1 for MBP-1/TBBO
)

func runData() {
	fmt.Println(">>> INGESTION: DBN (TBBO) -> QuantDev Binary <<<")

	files, _ := filepath.Glob("*.dbn")
	if len(files) == 0 {
		fmt.Println("[warn] No .dbn files found.")
		return
	}

	var wg sync.WaitGroup
	// Use I/O-specific concurrency instead of full CPUThreads to avoid
	// thrashing the filesystem and NVMe queue.
	sem := make(chan struct{}, IOThreads)

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
	fmt.Printf(" -> Converting %s...\n", filepath.Base(path))

	enc, err := NewEncoder(outPath)
	if err != nil {
		fmt.Printf("encoder init failed %s: %v\n", outPath, err)
		return
	}
	defer enc.Close()

	// 1. Read Header (DBN metadata prefix)
	headerBuf := make([]byte, 8)
	startOffset := int64(0)
	if n, _ := f.Read(headerBuf); n == 8 {
		if string(headerBuf[0:3]) == DBNMagic {
			metaLen := binary.LittleEndian.Uint32(headerBuf[4:8])
			startOffset = int64(8 + metaLen)
		}
	}
	f.Seek(startOffset, io.SeekStart)

	// 2. Streaming Loop
	const BufSize = 64 * 1024
	buf := make([]byte, BufSize)
	leftover := make([]byte, 0, 256)
	count := 0

	for {
		n, err := f.Read(buf)
		if n == 0 {
			break
		}

		data := buf[:n]
		if len(leftover) > 0 {
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

			rec := data[offset : offset+recSize]
			offset += recSize

			// rtype at byte 1
			if rec[1] != RTypeTBBO {
				continue
			}

			// Header area:
			// [0]  len (u8)
			// [1]  rtype (u8)
			// [2:4] publisher_id (u16 LE)
			// [4:8] instrument_id (u32 LE)
			// [8:16] ts_event (u64 LE)

			pubID := binary.LittleEndian.Uint16(rec[2:4])
			instrID := binary.LittleEndian.Uint32(rec[4:8])
			tsEvent := binary.LittleEndian.Uint64(rec[8:16])

			// Body:
			// [16:24] price (i64 fixed-9)
			// [24:28] size (u32)
			// [28]    action (char)
			// [29]    side (char: 'B','A','N')
			// [30]    flags (u8)
			// [31]    depth (u8)
			// [32:40] ts_recv (u64)
			// [40:44] ts_in_delta (i32)
			// [44:48] sequence (u32)
			// [48:56] bid_px_00 (i64)
			// [56:64] ask_px_00 (i64)
			// [64:68] bid_sz_00 (u32)
			// [68:72] ask_sz_00 (u32)
			// [72:76] bid_ct_00 (u32)
			// [76:80] ask_ct_00 (u32)

			pRaw := int64(binary.LittleEndian.Uint64(rec[16:24]))
			size := binary.LittleEndian.Uint32(rec[24:28])
			actionChar := int8(rec[28])
			sideChar := rec[29]
			flags := rec[30]
			depth := rec[31]

			var s int8
			switch sideChar {
			case 'B':
				s = 1
			case 'A':
				s = -1
			case 'N':
				s = 0
			default:
				s = 0
			}

			tsRecv := binary.LittleEndian.Uint64(rec[32:40])
			tsDelta := int32(binary.LittleEndian.Uint32(rec[40:44]))
			seq := binary.LittleEndian.Uint32(rec[44:48])

			bpRaw := int64(binary.LittleEndian.Uint64(rec[48:56]))
			apRaw := int64(binary.LittleEndian.Uint64(rec[56:64]))
			bs := binary.LittleEndian.Uint32(rec[64:68])
			as := binary.LittleEndian.Uint32(rec[68:72])
			bc := binary.LittleEndian.Uint32(rec[72:76])
			ac := binary.LittleEndian.Uint32(rec[76:80])

			// Skip Null/placeholder prices (Databento uses i64::MAX as sentinel)
			if pRaw == 9223372036854775807 {
				continue
			}

			_ = enc.AddRow(
				pubID,
				instrID,
				tsEvent,
				tsRecv,
				tsDelta,
				pRaw,
				size,
				s,
				actionChar,
				flags,
				depth,
				seq,
				bpRaw,
				apRaw,
				bs,
				as,
				bc,
				ac,
			)
			count++
		}

		if err == io.EOF {
			break
		}
	}

	if count == 0 {
		fmt.Printf("   [warn] no TBBO records written for %s\n", filepath.Base(path))
	}
}
