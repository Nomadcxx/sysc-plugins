package kdeconnect

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/image/draw"
)

// mockupArtDir holds the album-art composites, beside the recent-image
// thumbnails. Tests point it at a temp directory.
var mockupArtDir = filepath.Join(filepath.Dir(recentImageThumbDir), "mockups")

// composeMockup draws the album art at art into the screen of the kind's
// mockup and returns the cached PNG. The wire has no overlay, so the
// cover rides inside the mockup image itself. The screen is the dark
// region connected to the artwork's centre, which follows the rounded
// screen corners; the bezel, camera cutouts outside it and the transparent
// canvas keep the frame's own pixels. A new cover replaces the kind's old
// composite.
func composeMockup(kind, art string) (string, error) {
	asset, ok := mockupAsset(kind)
	if !ok {
		return "", fmt.Errorf("kdeconnect: no mockup artwork for %q", kind)
	}
	artInfo, err := os.Stat(art)
	if err != nil {
		return "", fmt.Errorf("kdeconnect: stat album art: %w", err)
	}
	assetInfo, err := os.Stat(asset)
	if err != nil {
		return "", fmt.Errorf("kdeconnect: stat mockup: %w", err)
	}
	key := fmt.Sprintf("%x", sha1.Sum([]byte(strings.Join([]string{
		asset, strconv.FormatInt(assetInfo.ModTime().UnixNano(), 10),
		art, strconv.FormatInt(artInfo.ModTime().UnixNano(), 10),
	}, "|"))))
	cached := filepath.Join(mockupArtDir, kind+"-"+key+".png")
	if _, err := os.Stat(cached); err == nil {
		return cached, nil
	}

	frame, err := decodeFile(asset)
	if err != nil {
		return "", err
	}
	cover, err := decodeFile(art)
	if err != nil {
		return "", err
	}
	// NRGBA like the artwork itself: a premultiplied copy rounds the
	// bezel's semi-transparent edge pixels.
	out := image.NewNRGBA(frame.Bounds())
	draw.Draw(out, out.Bounds(), frame, frame.Bounds().Min, draw.Src)
	mask, box := screenMask(out)
	if box.Empty() {
		return "", fmt.Errorf("kdeconnect: %s mockup has no screen", kind)
	}
	// Cover-fit: scale the art to fill the screen box, cropping the
	// overflow evenly, so a square cover fills a tall phone screen.
	filled := image.NewRGBA(image.Rect(0, 0, box.Dx(), box.Dy()))
	draw.CatmullRom.Scale(filled, filled.Bounds(), cover, coverCrop(cover.Bounds(), box.Dx(), box.Dy()), draw.Src, nil)
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x := box.Min.X; x < box.Max.X; x++ {
			if mask[y*out.Bounds().Dx()+x] {
				out.Set(x, y, filled.At(x-box.Min.X, y-box.Min.Y))
			}
		}
	}

	if err := os.MkdirAll(mockupArtDir, 0o755); err != nil {
		return "", fmt.Errorf("kdeconnect: mkdir %s: %w", mockupArtDir, err)
	}
	tmp := cached + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", fmt.Errorf("kdeconnect: create %s: %w", tmp, err)
	}
	err = png.Encode(f, out)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, cached)
	}
	if err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("kdeconnect: write %s: %w", cached, err)
	}
	// Drop the kind's earlier composites: one cover per mockup is live.
	if stale, _ := filepath.Glob(filepath.Join(mockupArtDir, kind+"-*.png")); len(stale) > 0 {
		for _, p := range stale {
			if p != cached {
				os.Remove(p)
			}
		}
	}
	return cached, nil
}

// screenMask flood-fills the dark, opaque screen region from the image's
// centre and returns it as a row-major mask with its bounding box.
func screenMask(img *image.NRGBA) ([]bool, image.Rectangle) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dark := func(x, y int) bool {
		c := img.NRGBAAt(b.Min.X+x, b.Min.Y+y)
		return c.A > 200 && max(c.R, c.G, c.B) < 40
	}
	mask := make([]bool, w*h)
	box := image.Rectangle{}
	seed := image.Pt(w/2, h/2)
	if !dark(seed.X, seed.Y) {
		return mask, box
	}
	stack := []image.Point{seed}
	mask[seed.Y*w+seed.X] = true
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		box = box.Union(image.Rect(p.X, p.Y, p.X+1, p.Y+1))
		for _, n := range [4]image.Point{{p.X + 1, p.Y}, {p.X - 1, p.Y}, {p.X, p.Y + 1}, {p.X, p.Y - 1}} {
			if n.X < 0 || n.Y < 0 || n.X >= w || n.Y >= h || mask[n.Y*w+n.X] || !dark(n.X, n.Y) {
				continue
			}
			mask[n.Y*w+n.X] = true
			stack = append(stack, n)
		}
	}
	return mask, box
}

// coverCrop is the centred source rectangle with the target's aspect.
func coverCrop(src image.Rectangle, w, h int) image.Rectangle {
	sw, sh := src.Dx(), src.Dy()
	if sw*h > sh*w { // source wider than the target: trim the sides
		cw := sh * w / h
		x := src.Min.X + (sw-cw)/2
		return image.Rect(x, src.Min.Y, x+cw, src.Max.Y)
	}
	ch := sw * h / w
	y := src.Min.Y + (sh-ch)/2
	return image.Rect(src.Min.X, y, src.Max.X, y+ch)
}

func decodeFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("kdeconnect: open %s: %w", path, err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("kdeconnect: decode %s: %w", path, err)
	}
	return img, nil
}

// localFilePath turns the daemon's file:// album-art URL into a path; any
// other scheme, or none, yields "".
func localFilePath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	return u.Path
}

var errNoMockup = errors.New("kdeconnect: device type has no mockup")

// mediaMockup is the composite for the device's current album art, or ""
// while the phone reports none or the type has no mockup.
func mediaMockup(dev *Device, artURL string) (string, error) {
	art := localFilePath(artURL)
	if art == "" {
		return "", nil
	}
	kind := MockupKind(dev)
	if kind == "" {
		return "", errNoMockup
	}
	return composeMockup(kind, art)
}
