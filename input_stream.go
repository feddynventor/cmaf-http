package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"math" // just for logging
	"sync"
	"time"

	"github.com/justincormack/go-memfd"
	"golang.org/x/sys/unix"
)

type Fragment struct {
	moof       []byte       `json:"-"`
	fd         *memfd.Memfd `json:"-"`
	ByteLength uint32       `json:"size"`
	Sequence   uint32       `json:"seq"`
	Pts        float32      `json:"pts"`
	Keyframe   bool         `json:"-"`
	IFrameSize uint32       `json:"iframe"`
}

type InputStream struct {
	repr            *Representation
	fragments       sync.Map
	lastSeqNumber   uint32
	timescale       uint32
	moov            []byte
	timestamp       time.Time
	fragmentsWindow *CircularBuffer[Fragment]
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

				iframebytes := GetIFrameSize(fullAtom)
				isIFrame := "X" // just debugging
				if iframebytes > 0 {
					isIFrame = "I" // just debugging
					fragment.(*Fragment).IFrameSize = iframebytes
					fragment.(*Fragment).Keyframe = true
					}
				}

				stream.fragmentsWindow.Add(fragment.(*Fragment))
			}
			break
		default:
		}
	}
}
