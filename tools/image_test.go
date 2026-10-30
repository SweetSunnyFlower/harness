package tools

import "testing"

func TestDetectSupportedImageMimeType(t *testing.T) {
	if got := DetectSupportedImageMimeType([]byte{0xff, 0xd8, 0xff, 0xe0}); got != "image/jpeg" {
		t.Fatalf("jpeg: got %q", got)
	}
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	png = append(png, 0, 0, 0, 13)
	png = append(png, []byte("IHDR")...)
	if got := DetectSupportedImageMimeType(png); got != "image/png" {
		t.Fatalf("png: got %q", got)
	}
	if got := DetectSupportedImageMimeType([]byte("hello")); got != "" {
		t.Fatalf("text: got %q", got)
	}
}
