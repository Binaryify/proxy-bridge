package main

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"proxybridge/internal/apps"
	"proxybridge/locales"
)

// TestSnapshots draws the panel to PNGs in $PROXYBRIDGE_SNAPSHOTS, to
// look at layout changes:
//
//	PROXYBRIDGE_SNAPSHOTS=/tmp/shots go test -run Snapshots .
//	PROXYBRIDGE_SNAPSHOTS=/tmp/shots PROXYBRIDGE_LOCALE=en go test -run Snapshots .
func TestSnapshots(t *testing.T) {
	dir := os.Getenv("PROXYBRIDGE_SNAPSHOTS")
	if dir == "" {
		t.Skip("PROXYBRIDGE_SNAPSHOTS not set")
	}
	os.MkdirAll(dir, 0o755)
	// PROXYBRIDGE_LOCALE=en draws it in English.
	if tag := os.Getenv("PROXYBRIDGE_LOCALE"); tag != "" {
		inLocale(t, tag)
	}
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
	f.click(locales.TurnOn())
	shot(f, "error")

	f.a.s.Address, f.a.s.Kind = "proxy.example.com:1080", 2
	f.click(locales.TurnOn())
	f.settle(2)
	shot(f, "all-on")
	f.a.quit()

	f = newFixture(t)
	f.tt.SetScale(scale)
	f.a.s.Address, f.a.s.Kind = "proxy.example.com:443", 1
	// Apps most Macs have, rather than this Mac's.
	f.sys.apps = nil
	for _, name := range []string{"Discord", "Figma", "Google Chrome", "Notion", "Safari", "Slack", "Spotify", "Telegram", "Terminal", "Visual Studio Code", "WeChat", "Xcode", "Zoom"} {
		f.sys.apps = append(f.sys.apps, apps.App{Path: "/Applications/" + name + ".app", Name: name})
	}
	f.click(locales.ModeApps())
	f.settle(1)
	for _, name := range []string{"Google Chrome", "Slack", "Visual Studio Code"} {
		f.a.choose("/Applications/"+name+".app", true)
	}
	f.click(locales.TurnOn())
	f.settle(2)
	shot(f, "apps-on")
	f.click(locales.TabApps())
	f.settle(1)
	shot(f, "app-list")
	f.a.quit()
}
