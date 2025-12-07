package main

import (
	"encoding/binary"
	"io"
	"os"
)

const (
	// Bump the magic so we can distinguish from the old on-disk layout.
	MagicGNC  = "GNC4"
	ChunkSize = 64 * 1024 // rows per chunk
)

type Encoder struct {
	// Core fields
	tsEvent   []uint64
	tsRecv    []uint64
	tsInDelta []int32

	pxBuffer []float64
	szBuffer []float64

	sdBuffer    []int8
	acBuffer    []int8
	flBuffer    []uint8
	depthBuffer []uint8

	sqBuffer []uint32

	bpBuffer []float64
	apBuffer []float64

	bsBuffer  []float64
	asBuffer  []float64
	bcBuffer  []uint32
	acCBuffer []uint32

	// Identity
	pubBuffer  []uint16
	instBuffer []uint32

	totalRows    uint64
	chunkOffsets []uint64
	outFile      *os.File
}

func NewEncoder(path string) (*Encoder, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}

	// Reserve header space with zeros.
	zeroHeader := make([]byte, 64)
	if _, err := f.Write(zeroHeader); err != nil {
		f.Close()
		return nil, err
	}

	return &Encoder{
		tsEvent:   make([]uint64, 0, ChunkSize),
		tsRecv:    make([]uint64, 0, ChunkSize),
		tsInDelta: make([]int32, 0, ChunkSize),

		pxBuffer: make([]float64, 0, ChunkSize),
		szBuffer: make([]float64, 0, ChunkSize),

		sdBuffer:    make([]int8, 0, ChunkSize),
		acBuffer:    make([]int8, 0, ChunkSize),
		flBuffer:    make([]uint8, 0, ChunkSize),
		depthBuffer: make([]uint8, 0, ChunkSize),

		sqBuffer: make([]uint32, 0, ChunkSize),

		bpBuffer: make([]float64, 0, ChunkSize),
		apBuffer: make([]float64, 0, ChunkSize),

		bsBuffer:  make([]float64, 0, ChunkSize),
		asBuffer:  make([]float64, 0, ChunkSize),
		bcBuffer:  make([]uint32, 0, ChunkSize),
		acCBuffer: make([]uint32, 0, ChunkSize),

		pubBuffer:  make([]uint16, 0, ChunkSize),
		instBuffer: make([]uint32, 0, ChunkSize),

		outFile: f,
	}, nil
}

// AddRow ingests a single TBBO record into the current chunk.
// All price/size fields are converted once to float64 here and
// written as raw float64s on disk (no fixed-9 round-trip).
func (e *Encoder) AddRow(
	pubID uint16,
	instrID uint32,
	tsE uint64,
	tsR uint64,
	tsD int32,
	pxRaw int64,
	sz uint32,
	side int8,
	action int8,
	fl uint8,
	depth uint8,
	seq uint32,
	bpRaw int64,
	apRaw int64,
	bs uint32,
	as uint32,
	bc uint32,
	ac uint32,
) error {
	e.tsEvent = append(e.tsEvent, tsE)
	e.tsRecv = append(e.tsRecv, tsR)
	e.tsInDelta = append(e.tsInDelta, tsD)

	// Convert once from fixed-9 to float64.
	e.pxBuffer = append(e.pxBuffer, float64(pxRaw)*PxScale)
	e.szBuffer = append(e.szBuffer, float64(sz))

	e.sdBuffer = append(e.sdBuffer, side)
	e.acBuffer = append(e.acBuffer, action)
	e.flBuffer = append(e.flBuffer, fl)
	e.depthBuffer = append(e.depthBuffer, depth)

	e.sqBuffer = append(e.sqBuffer, seq)

	e.bpBuffer = append(e.bpBuffer, float64(bpRaw)*PxScale)
	e.apBuffer = append(e.apBuffer, float64(apRaw)*PxScale)

	e.bsBuffer = append(e.bsBuffer, float64(bs))
	e.asBuffer = append(e.asBuffer, float64(as))
	e.bcBuffer = append(e.bcBuffer, bc)
	e.acCBuffer = append(e.acCBuffer, ac)

	e.pubBuffer = append(e.pubBuffer, pubID)
	e.instBuffer = append(e.instBuffer, instrID)

	e.totalRows++
	if len(e.tsEvent) >= ChunkSize {
		return e.flushChunk()
	}
	return nil
}

func (e *Encoder) flushChunk() error {
	n := len(e.tsEvent)
	if n == 0 {
		return nil
	}

	offset, _ := e.outFile.Seek(0, io.SeekCurrent)
	e.chunkOffsets = append(e.chunkOffsets, uint64(offset))

	// Chunk length header (uint32)
	var scratch [4]byte
	binary.LittleEndian.PutUint32(scratch[:], uint32(n))
	if _, err := e.outFile.Write(scratch[:]); err != nil {
		return err
	}

	// Order must match decoder.go

	// Timing
	if _, err := e.outFile.Write(asBytes(e.tsEvent)); err != nil {
		return err
	}
	if _, err := e.outFile.Write(asBytes(e.tsRecv)); err != nil {
		return err
	}
	if _, err := e.outFile.Write(asBytes(e.tsInDelta)); err != nil {
		return err
	}

	// Prices and sizes (raw float64)
	if _, err := e.outFile.Write(asBytes(e.pxBuffer)); err != nil {
		return err
	}
	if _, err := e.outFile.Write(asBytes(e.szBuffer)); err != nil {
		return err
	}

	// Side, Action, Flags, Depth
	if _, err := e.outFile.Write(asBytes(e.sdBuffer)); err != nil {
		return err
	}
	if _, err := e.outFile.Write(asBytes(e.acBuffer)); err != nil {
		return err
	}
	if _, err := e.outFile.Write(asBytes(e.flBuffer)); err != nil {
		return err
	}
	if _, err := e.outFile.Write(asBytes(e.depthBuffer)); err != nil {
		return err
	}

	// Sequence
	if _, err := e.outFile.Write(asBytes(e.sqBuffer)); err != nil {
		return err
	}

	// BBO prices (float64)
	if _, err := e.outFile.Write(asBytes(e.bpBuffer)); err != nil {
		return err
	}
	if _, err := e.outFile.Write(asBytes(e.apBuffer)); err != nil {
		return err
	}

	// BBO sizes and counts
	if _, err := e.outFile.Write(asBytes(e.bsBuffer)); err != nil {
		return err
	}
	if _, err := e.outFile.Write(asBytes(e.asBuffer)); err != nil {
		return err
	}
	if _, err := e.outFile.Write(asBytes(e.bcBuffer)); err != nil {
		return err
	}
	if _, err := e.outFile.Write(asBytes(e.acCBuffer)); err != nil {
		return err
	}

	// Identity: publisher / instrument
	if _, err := e.outFile.Write(asBytes(e.pubBuffer)); err != nil {
		return err
	}
	if _, err := e.outFile.Write(asBytes(e.instBuffer)); err != nil {
		return err
	}

	// Reset slices (keep capacity)
	e.tsEvent = e.tsEvent[:0]
	e.tsRecv = e.tsRecv[:0]
	e.tsInDelta = e.tsInDelta[:0]

	e.pxBuffer = e.pxBuffer[:0]
	e.szBuffer = e.szBuffer[:0]

	e.sdBuffer = e.sdBuffer[:0]
	e.acBuffer = e.acBuffer[:0]
	e.flBuffer = e.flBuffer[:0]
	e.depthBuffer = e.depthBuffer[:0]

	e.sqBuffer = e.sqBuffer[:0]

	e.bpBuffer = e.bpBuffer[:0]
	e.apBuffer = e.apBuffer[:0]

	e.bsBuffer = e.bsBuffer[:0]
	e.asBuffer = e.asBuffer[:0]
	e.bcBuffer = e.bcBuffer[:0]
	e.acCBuffer = e.acCBuffer[:0]

	e.pubBuffer = e.pubBuffer[:0]
	e.instBuffer = e.instBuffer[:0]

	return nil
}

func (e *Encoder) Close() error {
	if len(e.tsEvent) > 0 {
		if err := e.flushChunk(); err != nil {
			return err
		}
	}
	if err := e.writeFooter(); err != nil {
		return err
	}
	return e.outFile.Close()
}

func (e *Encoder) writeFooter() error {
	footerPos, _ := e.outFile.Seek(0, io.SeekCurrent)

	// Chunk index: [u32 count][u64 offsets...]
	var scratch [4]byte
	binary.LittleEndian.PutUint32(scratch[:], uint32(len(e.chunkOffsets)))
	if _, err := e.outFile.Write(scratch[:]); err != nil {
		return err
	}
	if len(e.chunkOffsets) > 0 {
		if _, err := e.outFile.Write(asBytes(e.chunkOffsets)); err != nil {
			return err
		}
	}

	// Rewrite Header
	if _, err := e.outFile.Seek(0, io.SeekStart); err != nil {
		return err
	}
	header := make([]byte, 64)
	copy(header[0:4], MagicGNC)
	binary.LittleEndian.PutUint64(header[8:16], e.totalRows)
	binary.LittleEndian.PutUint64(header[24:32], uint64(footerPos))

	_, err := e.outFile.Write(header)
	return err
}
