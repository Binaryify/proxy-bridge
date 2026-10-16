package main

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"proxybridge/internal/apps"
)

// TestSnapshots draws the panel to PNGs in $PROXYBRIDGE_SNAPSHOTS, to
// look at layout changes:
//
//	PROXYBRIDGE_SNAPSHOTS=/tmp/shots go test -run Snapshots .
func TestSnapshots(t *testing.T) {
	dir := os.Getenv("PROXYBRIDGE_SNAPSHOTS")
	if dir == "" {
		t.Skip("PROXYBRIDGE_SNAPSHOTS not set")
	}
	os.MkdirAll(dir, 0o755)
	const scale = 2
	shot := func(f *fixture, name string) {
		f.frames()
		var img image.Image = f.tt.Image()
		if h := f.p.height * scale; h > 0 {
			img = f.tt.Image().SubImage(image.Rect(0, 0, img.Bounds().Dx(), h))
		}
		out, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		png.Encode(out, img)
		out.Close()
	}

	// The menu bar icon, off and on.
	for name, on := range map[string]bool{"tray-off": false, "tray-on": true} {
		os.WriteFile(filepath.Join(dir, name+".png"), trayIcon(on), 0o644)
	}

	f := newFixture(t)
	f.tt.SetScale(scale)
	shot(f, "off")

	f.a.s.Address = "1.2.3.4"
	f.click("开启")
	shot(f, "error")

	f.a.s.Address, f.a.s.Kind = "proxy.example.com:1080", 2
	f.click("开启")
	f.settle(2)
	shot(f, "all-on")
	f.a.quit()

	f = newFixture(t)
	f.tt.SetScale(scale)
	f.a.s.Address, f.a.s.Kind = "proxy.example.com:443", 1
	// The apps of this Mac, to see the list as it is.
	f.sys.apps = apps.List(apps.Dirs()...)
	f.click("指定应用")
	f.settle(1)
	for _, ap := range f.a.apps[2:min(5, len(f.a.apps))] {
		f.a.choose(ap.Path, true)
	}
	f.click("开启")
	f.settle(2)
	shot(f, "apps-on")
	f.click("应用列表")
	f.settle(1)
	shot(f, "app-list")
	f.a.quit()
}
