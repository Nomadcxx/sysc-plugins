package kdeconnect

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"os"
)

// decodeBoundedFile decodes an image file under the package's byte and pixel
// limits. The images that reach the plugin come from the phone, so a small
// compressed file must not be able to name a huge bitmap: the byte count is
// capped before the file is read, and the dimensions are read before the
// pixels are decoded. what names the image in the error.
func decodeBoundedFile(path string, what string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("kdeconnect: open %s: %w", path, err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, recentImageMaxSourceBytes+1))
	if err != nil {
		return nil, fmt.Errorf("kdeconnect: read %s: %w", path, err)
	}
	if int64(len(raw)) > recentImageMaxSourceBytes {
		return nil, fmt.Errorf("kdeconnect: %s exceeds the %d-byte %s source limit", path, recentImageMaxSourceBytes, what)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("kdeconnect: read image dimensions for %s: %w", path, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width) > recentImageMaxPixels/int64(cfg.Height) {
		return nil, fmt.Errorf("kdeconnect: %s exceeds the %d-pixel %s limit", path, recentImageMaxPixels, what)
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("kdeconnect: decode %s: %w", path, err)
	}
	return img, nil
}
