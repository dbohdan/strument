package workspace

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func sample(t *testing.T, encode func(*bytes.Buffer, image.Image) error, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	var buf bytes.Buffer
	if err := encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Real encoder output, not hand-built headers: the point of sniffing is to
// agree with what actually comes off a disk.
func TestSniffRealImages(t *testing.T) {
	for _, tc := range []struct {
		name   string
		encode func(*bytes.Buffer, image.Image) error
		media  string
	}{
		{"png", func(b *bytes.Buffer, i image.Image) error { return png.Encode(b, i) }, "image/png"},
		{"jpeg", func(b *bytes.Buffer, i image.Image) error { return jpeg.Encode(b, i, nil) }, "image/jpeg"},
		{"gif", func(b *bytes.Buffer, i image.Image) error { return gif.Encode(b, i, nil) }, "image/gif"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info, reason := SniffImage(sample(t, tc.encode, 64, 48))
			if reason != "" {
				t.Fatalf("refused a real %s: %s", tc.name, reason)
			}
			if info.MediaType != tc.media {
				t.Errorf("media type = %q, want %q", info.MediaType, tc.media)
			}
			if info.Width != 64 || info.Height != 48 {
				t.Errorf("size = %dx%d, want 64x48", info.Width, info.Height)
			}
		})
	}
}

// The extension is the one part of a file anyone can change by renaming it, so
// the bytes decide. A JPEG called .png must still be sent as a JPEG.
func TestSniffIgnoresWhatTheFileIsCalled(t *testing.T) {
	info, reason := SniffImage(sample(t, func(b *bytes.Buffer, i image.Image) error {
		return jpeg.Encode(b, i, nil)
	}, 8, 8))
	if reason != "" || info.MediaType != "image/jpeg" {
		t.Errorf("got %q / %q", info.MediaType, reason)
	}
}

func TestSniffRejectsNonImages(t *testing.T) {
	for name, data := range map[string][]byte{
		"text":     []byte("package main\n\nfunc main() {}\n"),
		"empty":    nil,
		"tiny":     []byte("PNG"),
		"zip-ish":  append([]byte("PK\x03\x04"), bytes.Repeat([]byte{0}, 40)...),
		"png lies": append(append([]byte{}, pngSignature...), bytes.Repeat([]byte{0}, 40)...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, reason := SniffImage(data); reason == "" {
				t.Error("accepted something that is not an image")
			}
		})
	}
}

// An APNG decodes as its first frame, so DecodeConfig cannot tell one apart and
// the refusal has to come from the chunk stream. Providers reject these, and
// their message says less than ours does.
func TestSniffRejectsAnimatedPNG(t *testing.T) {
	still := sample(t, func(b *bytes.Buffer, i image.Image) error { return png.Encode(b, i) }, 4, 4)
	apng := injectChunkBeforeIDAT(t, still, "acTL")

	if _, reason := SniffImage(still); reason != "" {
		t.Fatalf("the control still PNG was refused: %s", reason)
	}
	_, reason := SniffImage(apng)
	if reason == "" {
		t.Fatal("an animated PNG was accepted")
	}
	if !strings.Contains(reason, "APNG") {
		t.Errorf("the refusal does not say what is wrong: %s", reason)
	}
}

// injectChunkBeforeIDAT splices an empty chunk of the given type in front of
// the first IDAT, which is what makes a PNG an APNG.
func injectChunkBeforeIDAT(t *testing.T, data []byte, kind string) []byte {
	t.Helper()
	idx := bytes.Index(data, []byte("IDAT"))
	if idx < 4 {
		t.Fatal("no IDAT chunk in the sample PNG")
	}
	at := idx - 4 // back up over the length field
	chunk := make([]byte, 0, 12)
	chunk = binary.BigEndian.AppendUint32(chunk, 0)
	chunk = append(chunk, kind...)
	chunk = binary.BigEndian.AppendUint32(chunk, 0) // CRC, unchecked here
	return append(append(append([]byte{}, data[:at]...), chunk...), data[at:]...)
}

// WebP is not in the standard library, so its header is read directly and the
// three variants disagree about where the size lives.
func TestSniffWebPVariants(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
		w, h int
	}{
		{"VP8X", append([]byte("VP8X"), []byte{0, 0, 0, 0, 0, 0, 0, 0, 0x3f, 0, 0, 0x1f, 0, 0}...), 64, 32},
		{"VP8L", append([]byte("VP8L"), []byte{0, 0, 0, 0, 0x2f, 0x3f, 0x00, 0x08, 0x00, 0, 0, 0, 0, 0}...), 64, 33},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := append([]byte("RIFF\x00\x00\x00\x00WEBP"), tc.body...)
			data = append(data, bytes.Repeat([]byte{0}, 40)...)
			info, reason := SniffImage(data)
			if reason != "" {
				t.Fatalf("refused: %s", reason)
			}
			if info.MediaType != "image/webp" {
				t.Errorf("media type = %q", info.MediaType)
			}
			if info.Width != tc.w || info.Height != tc.h {
				t.Errorf("size = %dx%d, want %dx%d", info.Width, info.Height, tc.w, tc.h)
			}
		})
	}
}

// A corrupt chunk length must not walk the sniffer off the end of a file it has
// not decided to trust yet.
func TestAnimatedPNGScanSurvivesABadLength(t *testing.T) {
	data := append([]byte{}, pngSignature...)
	data = binary.BigEndian.AppendUint32(data, 0xffffffff)
	data = append(data, "bOGs"...)
	if isAnimatedPNG(data) {
		t.Error("a corrupt chunk length was read as an acTL")
	}
}
