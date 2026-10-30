package tools

import "os"

// DetectSupportedImageMimeType returns the image MIME type of a byte slice, or
// an empty string when the bytes are not a supported image.
func DetectSupportedImageMimeType(buf []byte) string {
	if startsWith(buf, []byte{0xff, 0xd8, 0xff}) {
		if len(buf) > 3 && buf[3] == 0xf7 {
			return ""
		}
		return "image/jpeg"
	}
	if startsWith(buf, []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}) {
		if !isPng(buf) || isAnimatedPng(buf) {
			return ""
		}
		return "image/png"
	}
	if startsWithASCII(buf, 0, "GIF87a") || startsWithASCII(buf, 0, "GIF89a") {
		return "image/gif"
	}
	if startsWithASCII(buf, 0, "RIFF") && startsWithASCII(buf, 8, "WEBP") {
		return "image/webp"
	}
	if startsWithASCII(buf, 0, "BM") && isBmp(buf) {
		return "image/bmp"
	}
	return ""
}

// DetectImageMimeTypeOf reads a file's header and detects its image type.
func DetectImageMimeTypeOf(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	header := make([]byte, 32)
	n, _ := f.Read(header)
	if n <= 0 {
		return ""
	}
	return DetectSupportedImageMimeType(header[:n])
}

func startsWith(buf, prefix []byte) bool {
	if len(buf) < len(prefix) {
		return false
	}
	for i := range prefix {
		if buf[i] != prefix[i] {
			return false
		}
	}
	return true
}

func startsWithASCII(buf []byte, offset int, text string) bool {
	if offset < 0 || len(buf) < offset+len(text) {
		return false
	}
	for i := 0; i < len(text); i++ {
		if buf[offset+i] != text[i] {
			return false
		}
	}
	return true
}

func isPng(buf []byte) bool {
	return len(buf) >= 16 && readUint32BE(buf, 8) == 13 && startsWithASCII(buf, 12, "IHDR")
}

func isAnimatedPng(buf []byte) bool {
	offset := 8
	for offset+8 <= len(buf) {
		chunkLength := int(readUint32BE(buf, offset))
		chunkType := offset + 4
		if startsWithASCII(buf, chunkType, "acTL") {
			return true
		}
		if startsWithASCII(buf, chunkType, "IDAT") {
			return false
		}
		next := offset + 8 + chunkLength + 4
		if next <= offset || next > len(buf) {
			return false
		}
		offset = next
	}
	return false
}

func isBmp(buf []byte) bool {
	if len(buf) < 26 {
		return false
	}
	declaredFileSize := readUint32LE(buf, 2)
	pixelDataOffset := readUint32LE(buf, 10)
	dibHeaderSize := readUint32LE(buf, 14)
	if declaredFileSize != 0 && declaredFileSize < 26 {
		return false
	}
	if pixelDataOffset < 14+dibHeaderSize {
		return false
	}
	if declaredFileSize != 0 && pixelDataOffset >= declaredFileSize {
		return false
	}
	var colorPlanes, bitsPerPixel uint32
	if dibHeaderSize == 12 {
		colorPlanes = uint32(readUint16LE(buf, 22))
		bitsPerPixel = uint32(readUint16LE(buf, 24))
	} else if dibHeaderSize >= 40 && dibHeaderSize <= 124 {
		if len(buf) < 30 {
			return false
		}
		colorPlanes = uint32(readUint16LE(buf, 26))
		bitsPerPixel = uint32(readUint16LE(buf, 28))
	} else {
		return false
	}
	if colorPlanes != 1 {
		return false
	}
	switch bitsPerPixel {
	case 1, 4, 8, 16, 24, 32:
		return true
	default:
		return false
	}
}

func readUint16LE(buf []byte, offset int) uint16 {
	if offset+1 >= len(buf) {
		return 0
	}
	return uint16(buf[offset]) | uint16(buf[offset+1])<<8
}

func readUint32BE(buf []byte, offset int) uint32 {
	if offset+3 >= len(buf) {
		return 0
	}
	return uint32(buf[offset])<<24 | uint32(buf[offset+1])<<16 | uint32(buf[offset+2])<<8 | uint32(buf[offset+3])
}

func readUint32LE(buf []byte, offset int) uint32 {
	if offset+3 >= len(buf) {
		return 0
	}
	return uint32(buf[offset]) | uint32(buf[offset+1])<<8 | uint32(buf[offset+2])<<16 | uint32(buf[offset+3])<<24
}
