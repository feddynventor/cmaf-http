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
