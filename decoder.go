package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"unsafe"
)

// LoadQuantDev loads the binary file using unsafe pointer arithmetic for speed.
// It bypasses the reflection overhead of encoding/binary.
func LoadQuantDev(path string) (*TBBOColumns, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// 1. Read Header
	header := make([]byte, 64)
	if _, err := io.ReadFull(f, header); err != nil {
		return nil, fmt.Errorf("bad header: %v", err)
	}

	if string(header[0:4]) != MagicGNC {
		return nil, fmt.Errorf("invalid magic: expected %s, got %s", MagicGNC, string(header[0:4]))
	}

	totalRows := binary.LittleEndian.Uint64(header[8:16])
	footerPos := int64(binary.LittleEndian.Uint64(header[24:32]))

	// 2. Prepare Columns
	cols := TBBOPool.Get().(*TBBOColumns)
	cols.Reset()
	cols.EnsureCapacity(int(totalRows))

	// 3. Read Chunks - Optimized with bulk buffering
	// We read the chunk header, then reading the body is faster if we buffer huge blocks.
	// However, to keep it simple and robust within existing logic, we read chunk-by-chunk
	// but use UNSAFE casts to decode.

	f.Seek(64, io.SeekStart)
	curPos := int64(64)

	// Reusable scratch buffer for the largest chunks to avoid allocs
	// Max chunk is roughly 8192 rows * 80 bytes ~= 655KB. Safe 1MB buffer.
	scratch := make([]byte, 1024*1024)

	for curPos < footerPos {
		// Read Chunk Count (4 bytes)
		if _, err := io.ReadFull(f, scratch[:4]); err != nil {
			break
		}
		n := int(*(*uint32)(unsafe.Pointer(&scratch[0])))

		// Calculate byte size of this chunk's data
		// 2 * 8 (u64) + 1 * 4 (i32) + 3 * 8 (px) + 1 * 4 (sz) + 2 * 1 (sd/fl) + ...
		// We rely on the implicit structure.
		// Instead of calculating exact bytes, we just ReadFull arrays one by one using the scratch.

		// Helper to read directly into target slice using Unsafe Casts
		// NOTE: Go 1.25+ 'unsafe.Slice' is zero-cost.

		// TsEvent (uint64)
		bytesNeeded := n * 8
		io.ReadFull(f, scratch[:bytesNeeded])
		srcU64 := unsafe.Slice((*uint64)(unsafe.Pointer(&scratch[0])), n)
		cols.TsEvent = append(cols.TsEvent, srcU64...)

		// TsRecv (uint64)
		io.ReadFull(f, scratch[:bytesNeeded])
		srcU64 = unsafe.Slice((*uint64)(unsafe.Pointer(&scratch[0])), n)
		cols.TsRecv = append(cols.TsRecv, srcU64...)

		// TsInDelta (int32)
		bytesNeeded = n * 4
		io.ReadFull(f, scratch[:bytesNeeded])
		srcI32 := unsafe.Slice((*int32)(unsafe.Pointer(&scratch[0])), n)
		cols.TsInDelta = append(cols.TsInDelta, srcI32...)

		// Prices (Stored as Int64, need conversion to Float64)
		// We read as Int64, then SIMD loop to float.
		bytesNeeded = n * 8
		io.ReadFull(f, scratch[:bytesNeeded])
		srcI64 := unsafe.Slice((*int64)(unsafe.Pointer(&scratch[0])), n)
		// Manual append loop for conversion
		start := len(cols.Prices)
		cols.Prices = cols.Prices[:start+n] // extend slice
		dstF64 := cols.Prices[start:]
		for i := 0; i < n; i++ {
			dstF64[i] = float64(srcI64[i]) * PxScale
		}

		// Sizes (uint32)
		bytesNeeded = n * 4
		io.ReadFull(f, scratch[:bytesNeeded])
		srcU32 := unsafe.Slice((*uint32)(unsafe.Pointer(&scratch[0])), n)
		cols.Sizes = append(cols.Sizes, srcU32...)

		// Sides (int8)
		bytesNeeded = n * 1
		io.ReadFull(f, scratch[:bytesNeeded])
		srcI8 := unsafe.Slice((*int8)(unsafe.Pointer(&scratch[0])), n)
		cols.Sides = append(cols.Sides, srcI8...)

		// Flags (uint8)
		io.ReadFull(f, scratch[:bytesNeeded])
		srcU8 := unsafe.Slice((*uint8)(unsafe.Pointer(&scratch[0])), n)
		cols.Flags = append(cols.Flags, srcU8...)

		// BidPx
		bytesNeeded = n * 8
		io.ReadFull(f, scratch[:bytesNeeded])
		srcI64 = unsafe.Slice((*int64)(unsafe.Pointer(&scratch[0])), n)
		start = len(cols.BidPx)
		cols.BidPx = cols.BidPx[:start+n]
		dstF64 = cols.BidPx[start:]
		for i := 0; i < n; i++ {
			dstF64[i] = float64(srcI64[i]) * PxScale
		}

		// AskPx
		io.ReadFull(f, scratch[:bytesNeeded])
		srcI64 = unsafe.Slice((*int64)(unsafe.Pointer(&scratch[0])), n)
		start = len(cols.AskPx)
		cols.AskPx = cols.AskPx[:start+n]
		dstF64 = cols.AskPx[start:]
		for i := 0; i < n; i++ {
			dstF64[i] = float64(srcI64[i]) * PxScale
		}

		// BidSz
		bytesNeeded = n * 4
		io.ReadFull(f, scratch[:bytesNeeded])
		srcU32 = unsafe.Slice((*uint32)(unsafe.Pointer(&scratch[0])), n)
		cols.BidSz = append(cols.BidSz, srcU32...)

		// AskSz
		io.ReadFull(f, scratch[:bytesNeeded])
		srcU32 = unsafe.Slice((*uint32)(unsafe.Pointer(&scratch[0])), n)
		cols.AskSz = append(cols.AskSz, srcU32...)

		// BidCt
		io.ReadFull(f, scratch[:bytesNeeded])
		srcU32 = unsafe.Slice((*uint32)(unsafe.Pointer(&scratch[0])), n)
		cols.BidCt = append(cols.BidCt, srcU32...)

		// AskCt
		io.ReadFull(f, scratch[:bytesNeeded])
		srcU32 = unsafe.Slice((*uint32)(unsafe.Pointer(&scratch[0])), n)
		cols.AskCt = append(cols.AskCt, srcU32...)

		curPos, _ = f.Seek(0, io.SeekCurrent)
	}

	cols.Count = len(cols.Prices)
	return cols, nil
}
