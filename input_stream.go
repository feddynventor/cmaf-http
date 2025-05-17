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
	keyframes       []*Fragment // so that you do not traverse the sync.Map at every Manifest request (locking)
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

			fmt.Println(stream.repr.Id, "# Received moov atom at", stream.timestamp, "with resolution", stream.repr.Width, "x", stream.repr.Height, "and timescale", stream.timescale)
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
			break

		case "mdat":
			if fragment, ok := stream.fragments.Load(stream.lastSeqNumber); ok { // handles synchronization and semaphores internally
				// `interface{}` to Concrete Type `*Fragment`
				fragment.(*Fragment).ByteLength += atomSize

				file, _ := memfd.Create()
				file.Write(fragment.(*Fragment).moof)
				file.Write(fullAtom)
				file.SetImmutable()
				file.Seek(0, io.SeekStart)
				file.SetSize(int64(fragment.(*Fragment).ByteLength))
				fragment.(*Fragment).fd = file

				pts := (fragment.(*Fragment).Pts)
				iframebytes := GetIFrameSize(fullAtom)
				isIFrame := "X" // just debugging
				if iframebytes > 0 {
					isIFrame = "I" // just debugging
					fragment.(*Fragment).IFrameSize = iframebytes
					fragment.(*Fragment).Keyframe = true
					stream.AddKeyframe(fragment.(*Fragment))
					if len(stream.keyframes) > 1 && stream.lastSeqNumber > config.Ingester.HeapSize && stream.keyframes[0].Sequence < (stream.lastSeqNumber-config.Ingester.HeapSize) {
						deleteRange(
							&stream.fragments,
							stream.keyframes[0].Sequence,
							stream.keyframes[1].Sequence-1,
							func(v interface{}) {
								unix.Close(int(v.(*Fragment).fd.Fd()))
								v.(*Fragment).fd.Unmap()
							},
						)
					}
				}

				if stream.repr.Log == true {
					fmt.Printf("%s - Repr %s\tFrag %d\tPTS %02d:%02d\tSize %d [%d]\n", isIFrame, stream.repr.Id, fragment.(*Fragment).Sequence, int(pts/60), int(math.Mod(float64(pts), 60)), fragment.(*Fragment).ByteLength, iframebytes)
				}

				stream.fragmentsWindow.Add(fragment.(*Fragment))
			}
			break
		default:
		}
	}
}

func (stream *InputStream) GetPlayableFragment(index uint32) (*Fragment, int) {
	currentKey := index
	for {
		if val, ok := stream.fragments.Load(currentKey); ok {
			if val.(*Fragment).Keyframe == true {
				return val.(*Fragment), int(currentKey)
			}
		} else {
			return nil, 0
		}
		currentKey--
	}
}

func (stream *InputStream) GetLastFragment() *Fragment {
	if val, ok := stream.fragments.Load(stream.lastSeqNumber); ok {
		return val.(*Fragment)
	}
	return nil
}

// get all the fragments starting from the given one until you find a keyframe fragment (not to include, as it's the next one)
// return the number of fragments of the segment
func (stream *InputStream) GetNextFragments(keyframe *Fragment) ([]*Fragment, int) {
	var fragments []*Fragment
	currentKey := keyframe.Sequence + 1
	for {
		if val, ok := stream.fragments.Load(currentKey); ok {
			if val.(*Fragment).Keyframe == true {
				break
			}
			fragments = append(fragments, val.(*Fragment))
		} else {
			return nil, 0
		}
		currentKey++
	}
	return append([]*Fragment{keyframe}, fragments...), len(fragments) + 1 // include the keyframe
}

// method to add a keyframe fragment to the array and that trims the array if PTS is too old
func (stream *InputStream) AddKeyframe(frag *Fragment) {
	stream.keyframes = append(stream.keyframes, frag)
	if stream.keyframes[0].Pts < frag.Pts-float32(config.Ingester.HeapSize) {
		stream.keyframes = stream.keyframes[1:]
	}
}

// deletes all keys in the range [min, max] inclusive
func deleteRange(m *sync.Map, min, max uint32, callback func(interface{})) {
	m.Range(func(key, value interface{}) bool {
		k := key.(uint32)
		if k >= min && k <= max {
			callback(value)
			m.Delete(k)
		}
		return true
	})
}
