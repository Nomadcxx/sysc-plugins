package kdeconnect

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/image/draw"
)

// mockupArtDir holds the album-art composites, beside the recent-image
// thumbnails. init() fills it in through cacheDirs, and leaves it empty when
// there is no user cache directory. Tests point it at a temp directory.
var mockupArtDir string

// albumArtRoot is the daemon's album-art cache. A localAlbumArtUrl outside
// it is ignored: the value arrives over the session bus and may name any
// file the user can read. Set with mockupArtDir; tests point it at a temp
// directory.
var albumArtRoot string

// composeMockup draws the album art at art, which must be a regular file
// under artRoot, into the screen of the kind's
// mockup and returns the cached PNG. The wire has no overlay, so the
// cover rides inside the mockup image itself. The screen is the dark
// region connected to the artwork's centre, which follows the rounded
// screen corners; the bezel, camera cutouts outside it and the transparent
// canvas keep the frame's own pixels. A new cover replaces the kind's old
// composite.
func composeMockup(kind, art, artRoot string) (string, error) {
	asset, ok := mockupAsset(kind)
	if !ok {
		return "", fmt.Errorf("kdeconnect: no mockup artwork for %q", kind)
	}
	artFile, artInfo, err := openUnder(art, artRoot, "album art")
	if err != nil {
		return "", err
	}
	defer artFile.Close()
	assetInfo, err := os.Stat(asset)
	if err != nil {
		return "", fmt.Errorf("kdeconnect: stat mockup: %w", err)
	}
	key := fmt.Sprintf("%x", sha1.Sum([]byte(strings.Join([]string{
		asset, strconv.FormatInt(assetInfo.ModTime().UnixNano(), 10),
		art, strconv.FormatInt(artInfo.ModTime().UnixNano(), 10),
	}, "|"))))
	if err := ensurePrivateDir(mockupArtDir); err != nil {
		return "", err
	}
	cached := filepath.Join(mockupArtDir, kind+"-"+key+".png")
	if cacheHit(cached, pngMagic) {
		return cached, nil
	}

	frame, err := decodeBoundedFile(asset, "mockup artwork")
	if err != nil {
		return "", err
	}
	cover, err := decodeBounded(artFile, art, "album art")
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

	if err := writeCacheFile(cached, func(w io.Writer) error { return png.Encode(w, out) }); err != nil {
		return "", err
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

// localFilePath turns the daemon's file:// album-art URL into a path; any
// other scheme, none, or a remote host yields "".
func localFilePath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") {
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
	return composeMockup(kind, art, albumArtRoot)
}
