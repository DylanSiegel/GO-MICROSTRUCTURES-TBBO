package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"
	"weak" // Go 1.24+ feature
)

var (
	sharedCacheMu sync.Mutex
	// Use weak.Pointer to allow GC to reclaim cache if pressure is high
	sharedCache = make(map[string]weak.Pointer[TBBOColumns])
)

func LoadQuantDevShared(path string) (*TBBOColumns, error) {
	sharedCacheMu.Lock()
	defer sharedCacheMu.Unlock()

	if wp, ok := sharedCache[path]; ok {
		if ptr := wp.Value(); ptr != nil {
			return ptr, nil
		}
		delete(sharedCache, path) // GC collected it
	}

	cols := &TBBOColumns{}
	if err := loadFromFile(path, cols); err != nil {
		return nil, err
	}
	sharedCache[path] = weak.Make(cols)
	return cols, nil
}

func LoadQuantDev(path string) (*TBBOColumns, error) {
	cols := TBBOPool.Get().(*TBBOColumns)
	if err := loadFromFile(path, cols); err != nil {
		TBBOPool.Put(cols)
		return nil, err
	}
	return cols, nil
}

// readFullInto reads exactly len(buf) elements of type T into buf.
func readFullInto[T any](r io.Reader, buf []T) error {
	if len(buf) == 0 {
		return nil
	}
	_, err := io.ReadFull(r, asBytes(buf))
	return err
}

func loadFromFile(path string, cols *TBBOColumns) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	header := make([]byte, 64)
	if _, err := io.ReadFull(f, header); err != nil {
		return fmt.Errorf("bad header: %w", err)
	}

	if string(header[0:4]) != MagicGNC {
		return fmt.Errorf("unsupported quantdev magic %q (expected %q); re-run data conversion",
			header[0:4], MagicGNC)
	}

	totalRows := binary.LittleEndian.Uint64(header[8:16])
	// footerPos := binary.LittleEndian.Uint64(header[24:32]) // currently unused

	// Defensive: avoid overflowing int on weird files.
	maxInt := uint64(^uint(0) >> 1)
	if totalRows > maxInt {
		return fmt.Errorf("quantdev file too large: %d rows", totalRows)
	}
	nRows := int(totalRows)

	cols.Reset()

	// -------------------------------------------------------------------------
	// Critical change: reuse pooled backing arrays instead of allocating fresh.
	// -------------------------------------------------------------------------
	if nRows > 0 {
		cols.PublisherID = resize(cols.PublisherID, nRows)
		cols.InstrumentID = resize(cols.InstrumentID, nRows)

		cols.TsEvent = resize(cols.TsEvent, nRows)
		cols.TsRecv = resize(cols.TsRecv, nRows)
		cols.TsInDelta = resize(cols.TsInDelta, nRows)

		cols.Prices = resize(cols.Prices, nRows)
		cols.Sizes = resize(cols.Sizes, nRows)
		cols.Sides = resize(cols.Sides, nRows)
		cols.Actions = resize(cols.Actions, nRows)
		cols.Flags = resize(cols.Flags, nRows)
		cols.Depth = resize(cols.Depth, nRows)
		cols.Sequences = resize(cols.Sequences, nRows)

		cols.BidPx = resize(cols.BidPx, nRows)
		cols.AskPx = resize(cols.AskPx, nRows)
		cols.BidSz = resize(cols.BidSz, nRows)
		cols.AskSz = resize(cols.AskSz, nRows)
		cols.BidCt = resize(cols.BidCt, nRows)
		cols.AskCt = resize(cols.AskCt, nRows)
	} else {
		// Ensure zero-length slices if file is empty.
		cols.PublisherID = cols.PublisherID[:0]
		cols.InstrumentID = cols.InstrumentID[:0]
		cols.TsEvent = cols.TsEvent[:0]
		cols.TsRecv = cols.TsRecv[:0]
		cols.TsInDelta = cols.TsInDelta[:0]
		cols.Prices = cols.Prices[:0]
		cols.Sizes = cols.Sizes[:0]
		cols.Sides = cols.Sides[:0]
		cols.Actions = cols.Actions[:0]
		cols.Flags = cols.Flags[:0]
		cols.Depth = cols.Depth[:0]
		cols.Sequences = cols.Sequences[:0]
		cols.BidPx = cols.BidPx[:0]
		cols.AskPx = cols.AskPx[:0]
		cols.BidSz = cols.BidSz[:0]
		cols.AskSz = cols.AskSz[:0]
		cols.BidCt = cols.BidCt[:0]
		cols.AskCt = cols.AskCt[:0]
	}

	// After header, all chunks are laid out as:
	// [u32 n][columns for n rows...], repeated, then footer index.
	if _, err := f.Seek(64, io.SeekStart); err != nil {
		return err
	}

	var lenBuf [4]byte
	pos := 0

	for pos < nRows {
		if _, err := io.ReadFull(f, lenBuf[:]); err != nil {
			return fmt.Errorf("reading chunk length: %w", err)
		}
		n := int(binary.LittleEndian.Uint32(lenBuf[:]))
		if n == 0 {
			continue
		}
		if pos+n > nRows {
			return fmt.Errorf("corrupt chunk length: pos=%d, n=%d, total=%d", pos, n, nRows)
		}

		// Window for this chunk
		i0, i1 := pos, pos+n

		// Order must match encoder.go

		// 1. Event TS
		if err := readFullInto(f, cols.TsEvent[i0:i1]); err != nil {
			return err
		}
		// 2. Recv TS
		if err := readFullInto(f, cols.TsRecv[i0:i1]); err != nil {
			return err
		}
		// 3. Delta
		if err := readFullInto(f, cols.TsInDelta[i0:i1]); err != nil {
			return err
		}
		// 4. Prices (float64)
		if err := readFullInto(f, cols.Prices[i0:i1]); err != nil {
			return err
		}
		// 5. Sizes (float64)
		if err := readFullInto(f, cols.Sizes[i0:i1]); err != nil {
			return err
		}
		// 6. Side (int8)
		if err := readFullInto(f, cols.Sides[i0:i1]); err != nil {
			return err
		}
		// 7. Action (int8)
		if err := readFullInto(f, cols.Actions[i0:i1]); err != nil {
			return err
		}
		// 8. Flags (u8)
		if err := readFullInto(f, cols.Flags[i0:i1]); err != nil {
			return err
		}
		// 9. Depth (u8)
		if err := readFullInto(f, cols.Depth[i0:i1]); err != nil {
			return err
		}
		// 10. Sequences (u32)
		if err := readFullInto(f, cols.Sequences[i0:i1]); err != nil {
			return err
		}
		// 11. BidPx (float64)
		if err := readFullInto(f, cols.BidPx[i0:i1]); err != nil {
			return err
		}
		// 12. AskPx (float64)
		if err := readFullInto(f, cols.AskPx[i0:i1]); err != nil {
			return err
		}
		// 13. BidSz (float64)
		if err := readFullInto(f, cols.BidSz[i0:i1]); err != nil {
			return err
		}
		// 14. AskSz (float64)
		if err := readFullInto(f, cols.AskSz[i0:i1]); err != nil {
			return err
		}
		// 15. BidCt (u32)
		if err := readFullInto(f, cols.BidCt[i0:i1]); err != nil {
			return err
		}
		// 16. AskCt (u32)
		if err := readFullInto(f, cols.AskCt[i0:i1]); err != nil {
			return err
		}
		// 17. Publisher IDs (u16)
		if err := readFullInto(f, cols.PublisherID[i0:i1]); err != nil {
			return err
		}
		// 18. Instrument IDs (u32)
		if err := readFullInto(f, cols.InstrumentID[i0:i1]); err != nil {
			return err
		}

		pos += n
	}

	if pos != nRows {
		return fmt.Errorf("row count mismatch: loaded=%d, expected=%d", pos, nRows)
	}

	cols.Count = nRows
	return nil
}
