package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/justincormack/go-memfd"
)

type Fragment struct {
	moof       []byte       `json:"-"`
	fd         *memfd.Memfd `json:"-"`
	ByteLength uint32       `json:"-"`
	Sequence   uint32       `json:"sequence"`
	Pts        float32      `json:"pts"`
}

type InputStream struct {
	repr          *Representation
	fragments     sync.Map
	lastFrag      *Fragment
	lastSeqNumber uint32
	timescale     uint32
	moov          []byte
	timestamp     time.Time
	sizesWindow   []uint32
}

func (stream *InputStream) Parse(data io.Reader) {
	// Each MP4 Fragment must start with MP4 header 4B + 4B
	atomHeader := make([]byte, 8)
	for {
		if _, err := io.ReadFull(data, atomHeader); err != nil {
			fmt.Println(stream.repr.Id, "# Error reading atom header:", err)
			return
		}
		atomSize := binary.BigEndian.Uint32(atomHeader[:4])
		atomType := string(atomHeader[4:8])
		// by specs, atom size includes header, hence each atom is minimum 8 Bytes
		if atomSize < 8 {
			fmt.Println(stream.repr.Id, "# Invalid atom", atomSize, atomType)
			return
		}
		atomData := make([]byte, atomSize-8)
		if _, err := io.ReadFull(data, atomData); err != nil {
			fmt.Println(stream.repr.Id, "# Error reading atom data:", err)
			return
		}

		if atomType != "mdat" && atomType != "moof" && atomType != "moov" {
			continue
		}
		fullAtom := append(atomHeader, atomData...) // new slice with full atom

		switch atomType {
		case "moov":
			stream.moov = fullAtom // TODO: assert, is this a copy?
			stream.timestamp = time.Now()

			parser := NewMP4Parser(stream.moov, nil)
			stream.repr.Width, stream.repr.Height = parser.GetResolution()
			stream.timescale = parser.GetVideoTimescale()

			fmt.Println(stream.repr.Id, "# Received moov atom at", stream.timestamp)
			break

		case "moof":
			p := NewMP4Parser(stream.moov, fullAtom)
			pts := p.GetPTS(stream.timescale)
			seq := p.GetSequenceNumber()

			stream.lastSeqNumber = seq
			stream.fragments.Store(seq, &Fragment{
				moof:       fullAtom, // underlying data in slices is always passed by reference
				ByteLength: uint32(atomSize),
				Sequence:   seq,
				Pts:        pts,
			})
			stream.fragments.Delete(stream.lastSeqNumber - 50)
			break

		case "mdat":
			if fragment, ok := stream.fragments.Load(stream.lastSeqNumber); ok { // handles synchronization and semaphores internally
				// `interface{}` to Concrete Type `*Fragment`
				fragment.(*Fragment).ByteLength += atomSize

				file, _ := memfd.Create()
				file.Write(fragment.(*Fragment).moof)
				file.Write(fullAtom)
				file.SetImmutable()
				// fragment.(*Fragment).data = file // TODO: representation specific fragment file descriptor
				file.Seek(0, io.SeekStart)
				file.SetSize(int64(fragment.(*Fragment).ByteLength))
				fragment.(*Fragment).fd = file

				stream.lastFrag = fragment.(*Fragment)

				pts := (int)(fragment.(*Fragment).Pts)
				fmt.Printf("Fragment %d, Size %d, PTS %02d:%02d\n", fragment.(*Fragment).Sequence, fragment.(*Fragment).ByteLength, pts/60, pts%60)

				if len(stream.sizesWindow) == 0 {
					stream.sizesWindow = make([]uint32, config.Ingester.Horizon)
					stream.sizesWindow = append(stream.sizesWindow, uint32(fragment.(*Fragment).ByteLength/1000))
				} else {
					stream.sizesWindow = append(stream.sizesWindow[1:], uint32(fragment.(*Fragment).ByteLength/1000))[:config.Ingester.Horizon]
				}
			}
			break
		default:
		}
	}
}
