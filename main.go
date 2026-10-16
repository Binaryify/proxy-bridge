// Proxy Bridge sends the traffic of every app, or of the apps chosen,
// through an HTTP, HTTPS or SOCKS5 proxy, by a virtual network interface.
// It lives in the menu bar.
package main

import (
	"log"
	"os"
	"strconv"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"

	"proxybridge/internal/apps"
	"proxybridge/internal/tun"
	"proxybridge/locales"
)

// chrome is the panel's height besides the scrolling content: arrow,
// header, tabs and footer, with a margin from the screen's edge.
const chrome = 230

// shell is the menu bar icon and the panel it opens. It lives on the main
// thread.
type shell struct {
	a      *app
	panel  *panel
	tray   *mygo.Tray
	flyout *mygo.Window // the open panel, or nil
	trayOn bool         // the state the icon shows
}

func main() {
	// Run as root by launchd, as the service of a user's copy of the app:
	//	com.binaryify.proxybridge.helper --service <uid> <app>
	if len(os.Args) == 4 && os.Args[1] == "--service" {
		uid, err := strconv.Atoi(os.Args[2])
		if err == nil {
			err = tun.RunService(uid, os.Args[3])
		}
		if err != nil {
			log.Fatal(err)
		}
		return
	}
	// The window is the user's: as root it would install the service for
	// root, which no app of the user's reaches.
	if os.Geteuid() == 0 {
		log.Fatal("Proxy Bridge must not run as root")
	}
	// Two instances would fight over the network.
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	// Signed updates from the GitHub releases, checked daily, in a window
	// of native UI.
	mygo.Use(native.Plugin)
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	mygo.App.OnWindowAllClosed(func() {}) // the panel is the only window: keep running as it closes
	// The Edit menu's roles paste into the address field.
	mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{{Role: mygo.RoleAppMenu}, {Role: mygo.RoleEditMenu}}))
	sh := &shell{}
	mygo.App.WhenReady(sh.ready)
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func (sh *shell) ready() {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		log.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	// What this app is, as it launched: if the file changes under it, it
	// must not install the new one as its service.
	version, err := tun.Version(exe)
	if err != nil {
		log.Fatal(err)
	}
	sh.a = newApp(dir, desktop{exe: exe, version: version}, sh.post)
	sh.panel = &panel{a: sh.a, height: 560, maxContent: 640, onHeight: sh.resize}

	sh.tray, err = mygo.NewTray(mygo.TrayOptions{Icon: trayIcon(false), IconIsTemplate: true, ToolTip: locales.AppName()})
	if err != nil {
		log.Fatal("tray: ", err)
	}
	// The menu is made as it pops up, in the language of the moment.
	menu := func() *mygo.Menu {
		return mygo.NewMenu([]*mygo.MenuItem{
			{Label: locales.MenuOpen(), Click: func(*mygo.MenuItem, *mygo.Window) { sh.open() }},
			{Label: locales.MenuCheckUpdates(), Click: func(*mygo.MenuItem, *mygo.Window) { updater.CheckForUpdates() }},
			mygo.Separator(),
			{Label: locales.MenuQuit(), Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
		})
	}
	sh.tray.OnClick(sh.toggle)
	sh.tray.OnRightClick(func() {
		sh.close()
		sh.tray.SetMenu(menu()) // for this popup only, so clicks keep toggling the panel
		defer sh.tray.SetMenu(nil)
		sh.tray.PopUpMenu()
	})
	// Launching the app again (Finder, Spotlight) opens its panel.
	mygo.App.OnSecondInstance(func([]string, string) { sh.open() })
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		sh.a.quit() // takes the TUN down
		sh.tray.Destroy()
	})
	sh.a.loadApps()
	// Open the panel at launch, so the icon is found.
	time.AfterFunc(300*time.Millisecond, func() { mygo.RunOnMain(sh.open) })
}

// post runs fn on the main thread, then shows the state on the icon and
// draws the panel again.
func (sh *shell) post(fn func()) {
	mygo.RunOnMain(func() {
		fn()
		if on := sh.a.on(); on != sh.trayOn {
			sh.trayOn = on
			sh.tray.SetIcon(trayIcon(on), true)
			sh.tray.SetToolTip(sh.toolTip())
		}
		if sh.flyout != nil {
			sh.flyout.Invalidate()
		}
	})
}

func (sh *shell) toolTip() string {
	if sh.trayOn {
		return locales.TrayOn()
	}
	return locales.AppName()
}

func (sh *shell) toggle() {
	if sh.flyout != nil {
		sh.close()
	} else {
		sh.open()
	}
}

func (sh *shell) open() {
	if sh.flyout != nil {
		return
	}
	icon := sh.tray.Bounds()
	area := mygo.Screen.DisplayNearestPoint(mygo.Point{X: icon.X + icon.Width/2, Y: icon.Y + icon.Height/2}).WorkArea
	sh.panel.maxContent = float32(max(area.Height-chrome, 200))
	sh.flyout = mygo.NewFlyout(mygo.FlyoutOptions{
		Tray:      sh.tray,
		Placement: mygo.PlacementBottom,
		Width:     panelWidth,
		Height:    min(sh.panel.height, area.Height),
		Focusable: true,
		Shadow:    true,
		Content:   ui.View(sh.panel.view),
	})
	sh.flyout.OnClosed(func() { sh.flyout = nil })
	sh.pointArrow()
}

func (sh *shell) close() {
	if sh.flyout != nil {
		sh.flyout.Close()
	}
}

// resize fits the panel to its content, which changes with the tab.
func (sh *shell) resize(height int) {
	if sh.flyout == nil {
		return
	}
	sh.flyout.SetSize(panelWidth, height)
	sh.pointArrow()
}

// pointArrow aims the arrow at the icon's middle, wherever the panel was
// placed against the screen's edges.
func (sh *shell) pointArrow() {
	icon, win := sh.tray.Bounds(), sh.flyout.Bounds()
	sh.panel.arrowX = float32(icon.X + icon.Width/2 - win.X)
	sh.flyout.Invalidate()
}

// desktop is the real system: the installed apps, and the service run as
// root by launchd, installed through the system's password dialog.
type desktop struct {
	exe, version string // this app's executable, and its hash at launch
}

func (desktop) Apps() []apps.App { return apps.List(apps.Dirs()...) }

func (d desktop) StartTUN(c tun.Config, installing func()) (session, error) {
	s, err := tun.Start(d.exe, d.version, c, installing)
	if err != nil {
		return nil, err // not a nil *tun.Session in an interface
	}
	return s, nil
}

func (desktop) ServiceInstalled() bool { return tun.Installed() }

func (desktop) UninstallService() error { return tun.Uninstall() }

func (desktop) CheckForUpdates() { updater.CheckForUpdates() }
