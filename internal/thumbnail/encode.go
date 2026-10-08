package thumbnail

import (
	"bytes"
	"errors"
	"fmt"
	"image"

	"github.com/HugoSmits86/nativewebp"
	"golang.org/x/image/webp"
)

// MaxBytes is the largest thumbnail file the repository accepts. The shell
// would take 2 MiB; this keeps catalog cards cheap to fetch.
const MaxBytes = 512 << 10

// Encode writes img as a lossless WebP.
func Encode(img image.Image) ([]byte, error) {
	var b bytes.Buffer
	if err := nativewebp.Encode(&b, img, nil); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Validate reports why data is not an acceptable thumbnail file: larger than
// MaxBytes, not a WebP, or not exactly Width by Height. The messages read as
// a continuation of "thumbnail.webp ".
func Validate(data []byte) error {
	if len(data) > MaxBytes {
		return fmt.Errorf("is %d bytes; keep it under %d", len(data), MaxBytes)
	}
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return errors.New("is not a WebP image")
	}
	cfg, err := webp.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("cannot be read as a WebP: %w", err)
	}
	if cfg.Width != Width || cfg.Height != Height {
		return fmt.Errorf("is %dx%d; it must be %dx%d", cfg.Width, cfg.Height, Width, Height)
	}
	return nil
}
