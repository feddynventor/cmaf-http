package main

// GetIFrameSize returns the size of the I-frame in an H.264 stream (from an MP4 mdat atom)
// Returns 0 if no I-frame is found
func GetIFrameSize(mdat []byte) uint32 {
	if len(mdat) < 8 {
		// log.Println("MP4 mdat data too small")
		return 0
	}

	// In MP4, H.264 data usually uses the AVCC format where:
	// - Each NAL unit is prefixed with its length (4 bytes)
	// - No start codes are used

	offset := 8 // Skip the standard 8-byte mdat header

	// Some mdat atoms have an extended size field if the first size field is 1
	if len(mdat) >= 16 && mdat[0] == 0 && mdat[1] == 0 && mdat[2] == 0 && mdat[3] == 1 {
		offset = 16 // Skip the extended header
	}

	if offset >= len(mdat) {
		// log.Println("No data after mdat header")
		return 0
	}

	data := mdat[offset:]

	// Process NAL units in AVCC format
	pos := uint32(0)
	for pos+4 < uint32(len(data)) {
		// Get NAL unit size (4 bytes)
		nalSize := uint32(data[pos])<<24 | uint32(data[pos+1])<<16 | uint32(data[pos+2])<<8 | uint32(data[pos+3])

		if nalSize <= 0 || pos+4+nalSize > uint32(len(data)) {
			// Invalid NAL size or would go beyond data
			// log.Printf("Invalid NAL size %d at position %d", nalSize, pos)
			break
		}

		// NAL header starts after the size field
		nalHeader := data[pos+4]
		nalType := nalHeader & 0x1F

		// NAL type 5 is IDR picture (I-frame)
		if nalType == 5 {
			// log.Printf("Found I-frame (NAL type 5) with size %d bytes", nalSize)
			return nalSize
		}

		// Move to next NAL unit
		pos += 4 + nalSize
	}

	// Try alternative format with start codes instead of length prefixes
	return searchWithStartCodes(data)
}

// searchWithStartCodes tries to find I-frames using start code delimiters
// This is used as a fallback if AVCC format detection fails
func searchWithStartCodes(data []byte) uint32 {
	pos := 0
	for pos < len(data)-5 {
		// Look for NAL unit start code
		if (pos+4 <= len(data) && data[pos] == 0 && data[pos+1] == 0 && data[pos+2] == 0 && data[pos+3] == 1) ||
			(pos+3 <= len(data) && data[pos] == 0 && data[pos+1] == 0 && data[pos+2] == 1) {

			startCodeSize := 3
			if pos+4 <= len(data) && data[pos+3] == 1 {
				startCodeSize = 4
			}

			nalStart := pos + startCodeSize
			if nalStart >= len(data) {
				break
			}

			nalType := data[nalStart] & 0x1F

			// NAL type 5 is IDR picture (I-frame)
			if nalType == 5 {
				// Find the end of this NAL unit
				nalEnd := nalStart + 1
				for nalEnd < len(data)-3 {
					if (data[nalEnd] == 0 && data[nalEnd+1] == 0 && data[nalEnd+2] == 0 && nalEnd+3 < len(data) && data[nalEnd+3] == 1) ||
						(data[nalEnd] == 0 && data[nalEnd+1] == 0 && data[nalEnd+2] == 1) {
						break
					}
					nalEnd++
				}

				frameSize := nalEnd - nalStart
				// log.Printf("Found I-frame with start codes, size: %d bytes", frameSize)
				return uint32(frameSize)
			}

			pos = nalStart
		} else {
			pos++
		}
	}

	// log.Println("No I-frame found in data")
	return 0
}
