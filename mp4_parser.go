package main

import (
	"encoding/binary"
)

type MP4Parser struct {
	moovData []byte
	moofData []byte
}

type Atom struct {
	Type string
	Size uint32
	Data []byte
}

func NewMP4Parser(moovData, moofData []byte) *MP4Parser {
	return &MP4Parser{moovData: moovData, moofData: moofData}
}

func (p *MP4Parser) GetVideoTimescale() uint32 {
	moovAtom, _ := p.findAtom(p.moovData, "moov")
	trakAtoms := p.findAllAtoms(moovAtom.Data, "trak")

	for _, trakAtom := range trakAtoms {
		mdiaAtom, _ := p.findAtom(trakAtom.Data, "mdia")
		hdlrAtom, _ := p.findAtom(mdiaAtom.Data, "hdlr")
		handlerType := string(hdlrAtom.Data[8:12])
		if handlerType == "vide" {
			mdhdAtom, _ := p.findAtom(mdiaAtom.Data, "mdhd")
			version := mdhdAtom.Data[0]
			var timescale uint32
			if version == 1 {
				timescale = binary.BigEndian.Uint32(mdhdAtom.Data[20:24])
			} else {
				timescale = binary.BigEndian.Uint32(mdhdAtom.Data[12:16])
			}
			return timescale
		}
	}
	return 0 // Return 0 if no video track is found
}

// Constants for sample flag bit positions according to ISO/IEC 14496-12 standard
const (
	// sample_depends_on values (bits 24-25)
	SAMPLE_DEPENDS_ON_MASK    = 0x03000000
	SAMPLE_DEPENDS_ON_UNKNOWN = 0x00000000 // Value 0: dependency unknown
	SAMPLE_DEPENDS_ON_OTHERS  = 0x01000000 // Value 1: depends on other samples
	SAMPLE_DEPENDS_ON_NONE    = 0x02000000 // Value 2: doesn't depend on others (I-frame)

	// sample_is_non_sync_sample (bit 16)
	SAMPLE_IS_NOT_SYNC = 0x00010000 // Value 1: not a sync sample
)

// isKeyframeBySampleFlags determines if a sample is a keyframe based on its flags
// following the ISO/IEC 14496-12 specification
func isKeyframeBySampleFlags(flags uint32) bool {
	// Method 1: Check sample_depends_on value (bits 24-25)
	// Value 2 means the sample doesn't depend on others (keyframe/I-frame)
	sampleDependsOn := flags & SAMPLE_DEPENDS_ON_MASK
	if sampleDependsOn == SAMPLE_DEPENDS_ON_NONE {
		return true
	}

	// Method 2: Check sample_is_non_sync_sample (bit 16)
	// Value 0 indicates this is a sync sample (keyframe/I-frame)
	if (flags & SAMPLE_IS_NOT_SYNC) == 0 {
		// Only use this method if sample_depends_on is unknown (0)
		// This prevents conflicts between the two methods
		if sampleDependsOn == SAMPLE_DEPENDS_ON_UNKNOWN {
			return true
		}
	}

	return false
}

func (p *MP4Parser) IsIFrame() bool {
	moofAtom, _ := p.findAtom(p.moofData, "moof")
	trafAtom, _ := p.findAtom(moofAtom.Data, "traf")
	trunAtom, _ := p.findAtom(trafAtom.Data, "trun")

	// Parse version and flags (1 byte for version, 3 bytes for flags)
	trunFlags := binary.BigEndian.Uint32(trunAtom.Data[0:4]) & 0x00FFFFFF

	// Get sample count (next 4 bytes after version and flags)
	sampleCount := binary.BigEndian.Uint32(trunAtom.Data[4:8])

	pos := 8

	// Check if first sample flags are present (bit 2 === 0x000004)
	if trunFlags&0x000004 != 0 {
		firstSampleFlags := binary.BigEndian.Uint32(trunAtom.Data[pos : pos+4])
		pos += 4

		if isKeyframeBySampleFlags(firstSampleFlags) {
			return true
		}
	}

	// Check if data offset is present (bit 0 === value 0x000001)
	if trunFlags&0x000001 != 0 {
		pos += 4 // Skip data offset
	}

	// present optional fields per sample
	hasDuration := trunFlags&0x000100 != 0
	hasSize := trunFlags&0x000200 != 0
	hasFlags := trunFlags&0x000400 != 0
	hasCompTimeOffset := trunFlags&0x000800 != 0

	// Iterate through samples - if per-sample flags are present
	if hasFlags {
		// Skip through samples until we find one with an I-frame flag
		for i := uint32(0); i < sampleCount; i++ {
			// Skip duration if present
			if hasDuration {
				pos += 4
			}

			// Skip size if present
			if hasSize {
				pos += 4
			}

			// Make sure we don't go out of bounds
			if pos+4 > len(trunAtom.Data) {
				return false //, fmt.Errorf("corrupt TRUN atom: data too short")
			}

			sampleFlags := binary.BigEndian.Uint32(trunAtom.Data[pos : pos+4])
			pos += 4

			if isKeyframeBySampleFlags(sampleFlags) {
				return true
			}

			// Skip composition time offset if present
			if hasCompTimeOffset {
				pos += 4
			}
		}
	}

	// If we reach here, we didn't find an I-frame
	return false
}

func (p *MP4Parser) GetPTS(timescale uint32) float32 {
	moofAtom, _ := p.findAtom(p.moofData, "moof")
	trafAtom, _ := p.findAtom(moofAtom.Data, "traf")
	tfdtAtom, _ := p.findAtom(trafAtom.Data, "tfdt")

	version := tfdtAtom.Data[0]
	var baseMediaDecodeTime uint64
	if version == 1 {
		baseMediaDecodeTime = binary.BigEndian.Uint64(tfdtAtom.Data[4:12])
	} else {
		baseMediaDecodeTime = uint64(binary.BigEndian.Uint32(tfdtAtom.Data[4:8]))
	}

	pts := float32(baseMediaDecodeTime) / float32(timescale)
	return pts
}

func (p *MP4Parser) GetSequenceNumber() uint32 {
	moofAtom, _ := p.findAtom(p.moofData, "moof")
	mfhdAtom, _ := p.findAtom(moofAtom.Data, "mfhd")
	sequenceNumber := binary.BigEndian.Uint32(mfhdAtom.Data[4:8])
	return sequenceNumber
}

func (p *MP4Parser) GetResolution() (uint32, uint32) {
	moovAtom, _ := p.findAtom(p.moovData, "moov")
	trakAtom, _ := p.findAtom(moovAtom.Data, "trak")
	tkhdAtom, _ := p.findAtom(trakAtom.Data, "tkhd")
	width := binary.BigEndian.Uint32(tkhdAtom.Data[76:80]) >> 16
	height := binary.BigEndian.Uint32(tkhdAtom.Data[80:84]) >> 16
	return width, height
}

func (p *MP4Parser) findAtom(data []byte, atomType string) (Atom, int) {
	offset := 0
	for offset < len(data) {
		atom, nextOffset := p.readAtom(data, offset)
		if atom.Type == atomType {
			return atom, nextOffset
		}
		offset = nextOffset
	}
	return Atom{}, -1
}

func (p *MP4Parser) readAtom(data []byte, offset int) (Atom, int) {
	size := binary.BigEndian.Uint32(data[offset : offset+4])
	atomType := string(data[offset+4 : offset+8])
	atomData := data[offset+8 : offset+int(size)]
	return Atom{Type: atomType, Size: size, Data: atomData}, offset + int(size)
}

func (p *MP4Parser) findAllAtoms(data []byte, atomType string) []Atom {
	var atoms []Atom
	offset := 0
	for offset < len(data) {
		atom, nextOffset := p.readAtom(data, offset)
		if atom.Type == atomType {
			atoms = append(atoms, atom)
		}
		offset = nextOffset
	}
	return atoms
}
