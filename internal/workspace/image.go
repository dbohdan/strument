package workspace

import (
	"bytes"
	"encoding/binary"
	"image"
	_ "image/gif"  // registers the GIF config decoder for SniffImage
	_ "image/jpeg" // registers the JPEG config decoder
	_ "image/png"  // registers the PNG config decoder
)

// ImageInfo describes an image file well enough to send it and to name it.
type ImageInfo struct {
	MediaType string
	Width     int
	Height    int
}

// SniffImage identifies an image from its bytes, returning a user-facing reason
// when they are not an image this harness can send.
//
// From the content, never from the extension. A .png that is really a JPEG is a
// wire error with a provider message the user cannot act on, and the extension
// is the one part of a file anyone can get wrong by renaming it. Pi and Kimi
// Code both sniff for the same reason.
//
// PNG, JPEG and GIF go through image.DecodeConfig, which both identifies the
// format and reads the dimensions without decoding any pixels. WebP is not in
// the standard library and its header is simple enough to read directly.
func SniffImage(data []byte) (ImageInfo, string) {
	if len(data) < 12 {
		return ImageInfo{}, "the file is too short to be an image"
	}

	if isWebP(data) {
		w, h := webpSize(data)
		return ImageInfo{MediaType: "image/webp", Width: w, Height: h}, ""
	}

	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return ImageInfo{}, "the file is not a PNG, JPEG, GIF or WebP image"
	}
	// An animated PNG decodes as its first frame, so DecodeConfig cannot tell
	// one apart. Providers reject them, and a rejection that arrives from the
	// API says less than this does.
	if format == "png" && isAnimatedPNG(data) {
		return ImageInfo{}, "animated PNGs (APNG) cannot be sent; export a single frame"
	}
	media, ok := map[string]string{
		"png":  "image/png",
		"jpeg": "image/jpeg",
		"gif":  "image/gif",
	}[format]
	if !ok {
		// Only reachable if another decoder gets registered in this package.
		return ImageInfo{}, format + " images cannot be sent"
	}
	return ImageInfo{MediaType: media, Width: cfg.Width, Height: cfg.Height}, ""
}

var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// isAnimatedPNG reports whether an acTL chunk appears before the first IDAT,
// which is what makes a PNG an APNG.
func isAnimatedPNG(data []byte) bool {
	if !bytes.HasPrefix(data, pngSignature) {
		return false
	}
	for off := len(pngSignature); off+8 <= len(data); {
		length := binary.BigEndian.Uint32(data[off:])
		kind := data[off+4 : off+8]
		switch {
		case bytes.Equal(kind, []byte("acTL")):
			return true
		case bytes.Equal(kind, []byte("IDAT")):
			return false
		}
		// 4 length + 4 type + payload + 4 CRC. Guard the overflow rather than
		// trusting a length field from a file we are still deciding to trust.
		next := off + 12 + int(length)
		if next <= off || next > len(data) {
			return false
		}
		off = next
	}
	return false
}

func isWebP(data []byte) bool {
	return bytes.HasPrefix(data, []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP"))
}

// webpSize reads the dimensions of the three WebP variants, returning zeroes
// when it cannot. Zero dimensions only cost the label its size; they never stop
// the image being sent.
func webpSize(data []byte) (int, int) {
	if len(data) < 30 {
		return 0, 0
	}
	switch {
	case bytes.Equal(data[12:16], []byte("VP8X")):
		// 24-bit little-endian, stored as one less than the real value.
		w := int(data[24]) | int(data[25])<<8 | int(data[26])<<16
		h := int(data[27]) | int(data[28])<<8 | int(data[29])<<16
		return w + 1, h + 1
	case bytes.Equal(data[12:16], []byte("VP8 ")):
		if len(data) < 30 {
			return 0, 0
		}
		return int(binary.LittleEndian.Uint16(data[26:28]) & 0x3fff),
			int(binary.LittleEndian.Uint16(data[28:30]) & 0x3fff)
	case bytes.Equal(data[12:16], []byte("VP8L")):
		if len(data) < 25 {
			return 0, 0
		}
		bits := binary.LittleEndian.Uint32(data[21:25])
		return int(bits&0x3fff) + 1, int((bits>>14)&0x3fff) + 1
	}
	return 0, 0
}
