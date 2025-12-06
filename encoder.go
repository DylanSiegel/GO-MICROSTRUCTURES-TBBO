package main

import (
	"encoding/binary"
	"io"
	"os"
)

// GNC3 Constants (Version bumped for format change)
const (
	MagicGNC  = "GNC3"
	ChunkSize = 8192 // Power of 2 aligns better with pages
)

type Encoder struct {
	// Buffers for fixed-width data
	tsEvent   []uint64
	tsRecv    []uint64
	tsInDelta []int32

	pxBuffer []float64 // Store normalized floats in RAM
	szBuffer []uint32
	sdBuffer []int8
	flBuffer []uint8

	bpBuffer []float64
	apBuffer []float64
	bsBuffer []uint32
	asBuffer []uint32
	bcBuffer []uint32
	acBuffer []uint32

	totalRows    uint64
	chunkOffsets []uint64
	outFile      *os.File
}

func NewEncoder(path string) (*Encoder, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	// Reserve header space (64 bytes)
	if _, err := f.Seek(64, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}

	return &Encoder{
		tsEvent:   make([]uint64, 0, ChunkSize),
		tsRecv:    make([]uint64, 0, ChunkSize),
		tsInDelta: make([]int32, 0, ChunkSize),
		pxBuffer:  make([]float64, 0, ChunkSize),
		szBuffer:  make([]uint32, 0, ChunkSize),
		sdBuffer:  make([]int8, 0, ChunkSize),
		flBuffer:  make([]uint8, 0, ChunkSize),
		bpBuffer:  make([]float64, 0, ChunkSize),
		apBuffer:  make([]float64, 0, ChunkSize),
		bsBuffer:  make([]uint32, 0, ChunkSize),
		asBuffer:  make([]uint32, 0, ChunkSize),
		bcBuffer:  make([]uint32, 0, ChunkSize),
		acBuffer:  make([]uint32, 0, ChunkSize),
		outFile:   f,
	}, nil
}

func (e *Encoder) AddRow(tsE, tsR uint64, tsD int32, px int64, sz uint32, side int8, fl uint8, bp, ap int64, bs, as, bc, ac uint32) error {
	e.tsEvent = append(e.tsEvent, tsE)
	e.tsRecv = append(e.tsRecv, tsR)
	e.tsInDelta = append(e.tsInDelta, tsD)

	e.pxBuffer = append(e.pxBuffer, float64(px)*PxScale)
	e.szBuffer = append(e.szBuffer, sz)
	e.sdBuffer = append(e.sdBuffer, side)
	e.flBuffer = append(e.flBuffer, fl)

	e.bpBuffer = append(e.bpBuffer, float64(bp)*PxScale)
	e.apBuffer = append(e.apBuffer, float64(ap)*PxScale)
	e.bsBuffer = append(e.bsBuffer, bs)
	e.asBuffer = append(e.asBuffer, as)
	e.bcBuffer = append(e.bcBuffer, bc)
	e.acBuffer = append(e.acBuffer, ac)

	e.totalRows++

	if len(e.tsEvent) >= ChunkSize {
		return e.flushChunk()
	}
	return nil
}

func (e *Encoder) Close() error {
	if e.outFile == nil {
		return nil
	}
	defer e.outFile.Close()

	if len(e.tsEvent) > 0 {
		if err := e.flushChunk(); err != nil {
			return err
		}
	}
	return e.writeFooter()
}

func (e *Encoder) flushChunk() error {
	offset, _ := e.outFile.Seek(0, io.SeekCurrent)
	e.chunkOffsets = append(e.chunkOffsets, uint64(offset))

	// Write chunk count
	binary.Write(e.outFile, binary.LittleEndian, uint32(len(e.tsEvent)))

	// RAW BINARY WRITES
	// NOTE: We convert floats back to int64 for compact disk storage

	binary.Write(e.outFile, binary.LittleEndian, e.tsEvent)
	binary.Write(e.outFile, binary.LittleEndian, e.tsRecv)
	binary.Write(e.outFile, binary.LittleEndian, e.tsInDelta)

	writeFloatAsInt64(e.outFile, e.pxBuffer) // Prices
	binary.Write(e.outFile, binary.LittleEndian, e.szBuffer)
	binary.Write(e.outFile, binary.LittleEndian, e.sdBuffer)
	binary.Write(e.outFile, binary.LittleEndian, e.flBuffer)

	writeFloatAsInt64(e.outFile, e.bpBuffer) // BidPx
	writeFloatAsInt64(e.outFile, e.apBuffer) // AskPx
	binary.Write(e.outFile, binary.LittleEndian, e.bsBuffer)
	binary.Write(e.outFile, binary.LittleEndian, e.asBuffer)
	binary.Write(e.outFile, binary.LittleEndian, e.bcBuffer)
	binary.Write(e.outFile, binary.LittleEndian, e.acBuffer)

	// Reset buffers
	e.tsEvent = e.tsEvent[:0]
	e.tsRecv = e.tsRecv[:0]
	e.tsInDelta = e.tsInDelta[:0]
	e.pxBuffer = e.pxBuffer[:0]
	e.szBuffer = e.szBuffer[:0]
	e.sdBuffer = e.sdBuffer[:0]
	e.flBuffer = e.flBuffer[:0]
	e.bpBuffer = e.bpBuffer[:0]
	e.apBuffer = e.apBuffer[:0]
	e.bsBuffer = e.bsBuffer[:0]
	e.asBuffer = e.asBuffer[:0]
	e.bcBuffer = e.bcBuffer[:0]
	e.acBuffer = e.acBuffer[:0]

	return nil
}

func (e *Encoder) writeFooter() error {
	footerPos, _ := e.outFile.Seek(0, io.SeekCurrent)

	binary.Write(e.outFile, binary.LittleEndian, uint32(len(e.chunkOffsets)))
	binary.Write(e.outFile, binary.LittleEndian, e.chunkOffsets)

	// Write Header
	e.outFile.Seek(0, io.SeekStart)
	header := make([]byte, 64)
	copy(header[0:4], MagicGNC)
	binary.LittleEndian.PutUint64(header[8:16], e.totalRows)
	// Other header fields can be zeroed or used for versioning
	binary.LittleEndian.PutUint64(header[24:32], uint64(footerPos))
	e.outFile.Write(header)

	return nil
}

// Helper to convert normalized floats back to raw int64 for disk
func writeFloatAsInt64(w io.Writer, data []float64) {
	buf := make([]int64, len(data))
	for i, v := range data {
		buf[i] = int64(v / PxScale)
	}
	binary.Write(w, binary.LittleEndian, buf)
}
