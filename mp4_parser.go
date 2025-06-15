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

const (
	IS_SYNC_SAMPLE = 0x02000000 // bit 25: sample is a sync sample (I-frame)
)

func NewMP4Parser(moovData, moofData []byte) *MP4Parser {
	return &MP4Parser{moovData: moovData, moofData: moofData}
}

func (p *MP4Parser) IsIFrame() bool {
	if len(p.moofData) == 0 {
		return false
	}

	moofAtom, _ := p.findAtom(p.moofData, "moof")
	if moofAtom.Type == "" {
		return false
	}
	trafAtom, _ := p.findAtom(moofAtom.Data, "traf")
	if trafAtom.Type == "" {
		return false
	}

	// Check tfhd (Track Fragment Header) for default sample flags
	tfhdAtom, _ := p.findAtom(trafAtom.Data, "tfhd")
	if tfhdAtom.Type != "" && p.checkTfhdSyncFlag(tfhdAtom.Data) {
		return true
	}

	// Check trun (Track Fragment Run) for sample-specific flags
	// Plus sanity check
	trunAtom, _ := p.findAtom(trafAtom.Data, "trun")
	if trunAtom.Type != "" && p.checkTrunSyncFlag(trunAtom.Data) {
		return true
	}

	return false
}

func (p *MP4Parser) checkTfhdSyncFlag(tfhdData []byte) bool {
	if len(tfhdData) < 8 {
		return false
	}

	flags := binary.BigEndian.Uint32(tfhdData[0:4]) & 0x00FFFFFF

	// Check if default-sample-flags is present (tfhd flag 0x000020)
	if flags&0x000020 == 0 {
		return false // No default sample flags present
	}

	offset := 8 // Skip version+flags(4) + track_ID(4)

	// Navigate through optional fields based on tfhd flags
	if flags&0x000001 != 0 { // base-data-offset present
		offset += 8
	}
	if flags&0x000002 != 0 { // sample-description-index present
		offset += 4
	}
	if flags&0x000008 != 0 { // default-sample-duration present
		offset += 4
	}
	if flags&0x000010 != 0 { // default-sample-size present
		offset += 4
	}

	if offset+4 <= len(tfhdData) {
		defaultSampleFlags := binary.BigEndian.Uint32(tfhdData[offset : offset+4])
		return (defaultSampleFlags & IS_SYNC_SAMPLE) != 0
	}

	return false
}

// checkTrunSyncFlag analyzes trun atom for sync sample flags
func (p *MP4Parser) checkTrunSyncFlag(trunData []byte) bool {
	if len(trunData) < 8 {
		return false
	}

	flags := binary.BigEndian.Uint32(trunData[0:4]) & 0x00FFFFFF
	sampleCount := binary.BigEndian.Uint32(trunData[4:8])

	if sampleCount == 0 {
		return false
	}

	offset := 8

	// Skip data_offset if present (trun flag 0x000001)
	if flags&0x000001 != 0 {
		offset += 4
	}

	// Check first_sample_flags if present (trun flag 0x000004)
	if flags&0x000004 != 0 {
		if offset+4 <= len(trunData) {
			firstSampleFlags := binary.BigEndian.Uint32(trunData[offset : offset+4])
			if (firstSampleFlags & IS_SYNC_SAMPLE) != 0 {
				return true
			}
		}
		offset += 4
	}

	// Check per-sample flags if present (trun flag 0x000400)
	if flags&0x000400 != 0 {
		// Calculate sample entry size based on trun flags
		sampleEntrySize := 0
		if flags&0x000100 != 0 { // sample_duration present
			sampleEntrySize += 4
		}
		if flags&0x000200 != 0 { // sample_size present
			sampleEntrySize += 4
		}
		if flags&0x000400 != 0 { // sample_flags present
			sampleEntrySize += 4
		}
		if flags&0x000800 != 0 { // sample_composition_time_offset present
			sampleEntrySize += 4
		}

		for i := uint32(0); i < sampleCount && offset+sampleEntrySize <= len(trunData); i++ {
			sampleFlagsOffset := offset

			// Navigate to sample_flags within this sample entry
			if flags&0x000100 != 0 { // skip sample_duration
				sampleFlagsOffset += 4
			}
			if flags&0x000200 != 0 { // skip sample_size
				sampleFlagsOffset += 4
			}

			// Now at sample_flags position
			if sampleFlagsOffset+4 <= len(trunData) {
				sampleFlags := binary.BigEndian.Uint32(trunData[sampleFlagsOffset : sampleFlagsOffset+4])
				if (sampleFlags & IS_SYNC_SAMPLE) != 0 {
					return true
				}
			}

			offset += sampleEntrySize
		}
	}

	return false
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
