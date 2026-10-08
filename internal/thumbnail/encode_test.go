package thumbnail

import (
	"image"
	"strings"
	"testing"
)

func TestValidateRejectsWhatIsNotAThumbnail(t *testing.T) {
	small, err := Encode(image.NewNRGBA(image.Rect(0, 0, 100, 100)))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"empty", nil, "not a WebP"},
		{"png header", []byte("\x89PNG\r\n\x1a\n0000000000"), "not a WebP"},
		{"wrong size", small, "100x100"},
		{"too big", append([]byte("RIFF\x00\x00\x00\x00WEBP"), make([]byte, MaxBytes)...), "keep it under"},
		{"truncated", []byte("RIFF\x00\x00\x00\x00WEBPVP8L"), "cannot be read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.data)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate = %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestValidateAcceptsAFullSizeWebP(t *testing.T) {
	data, err := Encode(image.NewNRGBA(image.Rect(0, 0, Width, Height)))
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(data); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
