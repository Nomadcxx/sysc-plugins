package kdeconnect

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// useRealMockups points the resolver at the shipped artwork and the
// composite cache at a temp directory. Sequential, as useMockupAssets is.
func useRealMockups(t *testing.T) string {
	t.Helper()
	assets, err := filepath.Abs("assets")
	if err != nil {
		t.Fatal(err)
	}
	prevAssets, prevCache := mockupAssetDir, mockupArtDir
	mockupAssetDir, mockupArtDir = assets, t.TempDir()
	t.Cleanup(func() { mockupAssetDir, mockupArtDir = prevAssets, prevCache })
	return mockupArtDir
}

// writeArt writes a solid-colour cover, the shape of the daemon's cached
// album art.
func writeArt(t *testing.T, path string, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, c)
		}
	}
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(out, img); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

func readPNG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestComposeMockupFillsOnlyTheScreen(t *testing.T) {
	cache := useRealMockups(t)
	art := filepath.Join(t.TempDir(), "cover.png")
	red := color.RGBA{R: 220, A: 255}
	writeArt(t, art, red)

	got, err := composeMockup("phone", art)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(got) != cache {
		t.Fatalf("composite %s is not in the cache %s", got, cache)
	}
	frame := readPNG(t, filepath.Join(mockupAssetDir, "phone.png"))
	out := readPNG(t, got)
	if out.Bounds() != frame.Bounds() {
		t.Fatalf("composite bounds %v, want the mockup's %v", out.Bounds(), frame.Bounds())
	}
	// The screen's centre carries the cover; the bezel and the transparent
	// canvas outside the rounded screen keep the frame's own pixels.
	if r, g, b, _ := out.At(55, 111).RGBA(); r>>8 != 220 || g != 0 || b != 0 {
		t.Fatalf("screen centre = %v, want the cover's red", out.At(55, 111))
	}
	for _, p := range []image.Point{{2, 111}, {55, 3}, {0, 0}, {7, 11}} {
		if out.At(p.X, p.Y) != frame.At(p.X, p.Y) {
			t.Fatalf("pixel %v = %v, want the frame's %v", p, out.At(p.X, p.Y), frame.At(p.X, p.Y))
		}
	}

	// The same cover is served from the cache; a new cover replaces the
	// old composite rather than piling up beside it.
	again, err := composeMockup("phone", art)
	if err != nil || again != got {
		t.Fatalf("recompose = %q, %v, want the cached %q", again, err, got)
	}
	writeArt(t, art, color.RGBA{B: 220, A: 255})
	later := time.Now().Add(time.Second)
	if err := os.Chtimes(art, later, later); err != nil {
		t.Fatal(err)
	}
	next, err := composeMockup("phone", art)
	if err != nil || next == got {
		t.Fatalf("new cover composite = %q, %v, want a fresh file", next, err)
	}
	if _, err := os.Stat(got); !os.IsNotExist(err) {
		t.Fatalf("stale composite %s still on disk", got)
	}
}

func TestComposeMockupRefusesMissingArt(t *testing.T) {
	useRealMockups(t)
	if _, err := composeMockup("phone", filepath.Join(t.TempDir(), "gone.png")); err == nil {
		t.Fatal("composed a mockup from a missing cover")
	}
	if _, err := composeMockup("tv", ""); err == nil {
		t.Fatal("composed a mockup for a type with no artwork")
	}
}

func TestDeviceMockupPrefersTheAlbumArtComposite(t *testing.T) {
	cache := useRealMockups(t)
	composite := filepath.Join(cache, "phone-cover.png")
	if err := os.WriteFile(composite, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	path, w, h, ok := deviceMockup(&Device{Type: "phone", MockupArt: composite})
	if !ok || path != composite || w != 111 || h != 225 {
		t.Fatalf("deviceMockup = %q,%d,%d,%v, want the composite at the mockup size", path, w, h, ok)
	}
	// A composite the cache lost falls back to the plain frame.
	if err := os.Remove(composite); err != nil {
		t.Fatal(err)
	}
	if path, _, _, _ := deviceMockup(&Device{Type: "phone", MockupArt: composite}); path != filepath.Join(mockupAssetDir, "phone.png") {
		t.Fatalf("deviceMockup = %q, want the plain frame", path)
	}
}

func TestMediaArtReachesTheSnapshot(t *testing.T) {
	useRealMockups(t)
	art := filepath.Join(t.TempDir(), "cover.png")
	writeArt(t, art, color.RGBA{G: 200, A: 255})

	bus := testBus()
	dev := bus.objects[devicePath("devA")]
	dev.ifaces[kdeDeviceIface]["supportedPlugins"] = dbus.MakeVariant(
		append(stringListOf(dev.ifaces[kdeDeviceIface]["supportedPlugins"]), "kdeconnect_mprisremote"))
	media := &fakeObject{ifaces: map[string]map[string]dbus.Variant{
		mprisremoteIface: {"localAlbumArtUrl": dbus.MakeVariant("")},
	}}
	bus.objects[pluginPath("devA", "mprisremote")] = media
	svc := newService(singleConnect(bus))
	defer svc.Close()

	snap := waitForSnapshot(t, svc, func(s Snapshot) bool { return s.Available && len(s.Devices) == 2 })
	if a := findDevice(snap, "devA"); a == nil || a.MockupArt != "" {
		t.Fatalf("no cover yet, device = %+v", a)
	}

	// A track change arrives as the plugin's PropertiesChanged.
	bus.mu.Lock()
	media.ifaces[mprisremoteIface]["localAlbumArtUrl"] = dbus.MakeVariant("file://" + art)
	bus.mu.Unlock()
	bus.inject(t, &dbus.Signal{
		Sender: kdeService, Path: pluginPath("devA", "mprisremote"),
		Name: propsIface + ".PropertiesChanged",
		Body: []any{mprisremoteIface, map[string]dbus.Variant{}, []string{}},
	})
	snap = waitForSnapshot(t, svc, func(s Snapshot) bool {
		a := findDevice(s, "devA")
		return a != nil && a.MockupArt != ""
	})
	if img := readPNG(t, findDevice(snap, "devA").MockupArt); img.Bounds().Dx() != 111 {
		t.Fatalf("composite width = %d, want the phone mockup's 111", img.Bounds().Dx())
	}
}

func findDevice(s Snapshot, id string) *Device {
	for i := range s.Devices {
		if s.Devices[i].ID == id {
			return &s.Devices[i]
		}
	}
	return nil
}

func TestMockupArtChangeIsStructural(t *testing.T) {
	t.Parallel()
	prev, next := pairedSnap(), pairedSnap()
	next.Devices[0].MockupArt = "/cache/phone-cover.png"
	if PanelDelta(prev, next) != nil {
		t.Fatal("a new cover patched the panel; the mockup image needs a full snapshot")
	}
}
