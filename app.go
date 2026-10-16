package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"proxybridge/internal/apps"
	"proxybridge/internal/tun"
	"proxybridge/internal/upstream"
)

// settings are what the user chose, kept between launches.
type settings struct {
	Kind    int      `json:"kind"`    // an index of upstream.Kinds
	Address string   `json:"address"` // host:port, with user:pass@ before it for a password
	Mode    int      `json:"mode"`    // modeAll or modeApps
	Apps    []string `json:"apps"`    // the bundles modeApps sends through the proxy
	Tab     int      `json:"tab"`     // the panel's tab
	Private bool     `json:"private"` // private addresses through the proxy too
}

const (
	modeAll  = iota // every app's traffic
	modeApps        // the chosen apps' traffic
)

// system is what the app changes outside itself; tests replace it.
type system interface {
	Apps() []apps.App // the installed apps; reads the disk
	// StartTUN starts the TUN through the service. When the service needs
	// installing it calls installing, then the system asks for the
	// password. It blocks.
	StartTUN(c tun.Config, installing func()) (session, error)
	ServiceInstalled() bool
	UninstallService() error // asks for the password; blocks
	CheckForUpdates()        // shows the update window
}

// session is a running TUN.
type session interface {
	Update(c tun.Config) error // changes its apps
	Wait(onEvent func(tun.Event))
	Close() error // blocks until it is down
}

// tone colors a status line.
type tone int

const (
	plain tone = iota
	good
	bad
)

// app is the window's state. It is read and changed on the UI thread;
// work in goroutines hands its results back through post.
type app struct {
	s    settings
	file string // where s is saved
	sys  system
	// post runs fn on the UI thread, then draws the window again.
	post func(fn func())

	apps    []apps.App // the installed apps, nil until read
	query   string     // filters apps by name
	appList ui.ListState

	addrErr string

	sess session    // nil while off
	cfg  tun.Config // what sess runs with
	via  upstream.Proxy
	busy bool // turning on or off
	// installing is while the service is installed, as the system asks
	// for the password, the first time only.
	installing bool

	// How the upstream answers, while on.
	probing  bool
	latency  int64 // ms
	probeErr string

	note     string // how the last action went
	noteTone tone
}

func newApp(dir string, sys system, post func(func())) *app {
	a := &app{
		s:    settings{Mode: modeAll},
		file: filepath.Join(dir, "settings.json"),
		sys:  sys,
		post: post,
	}
	if b, err := os.ReadFile(a.file); err == nil {
		json.Unmarshal(b, &a.s)
	}
	a.s.Kind = min(max(a.s.Kind, 0), len(upstream.Kinds)-1)
	a.s.Mode = min(max(a.s.Mode, modeAll), modeApps)
	a.s.Tab = min(max(a.s.Tab, 0), len(tabs)-1)
	return a
}

func (a *app) save() {
	b, _ := json.MarshalIndent(a.s, "", "  ")
	os.WriteFile(a.file, b, 0o600)
}

func (a *app) setNote(t tone, format string, args ...any) {
	a.note, a.noteTone = fmt.Sprintf(format, args...), t
}

// addressChanged takes the type from a pasted URL, as socks5://host:port.
func (a *app) addressChanged() {
	a.addrErr = ""
	scheme, rest, ok := strings.Cut(strings.TrimSpace(a.s.Address), "://")
	if !ok {
		return
	}
	if i := slices.Index(upstream.Kinds, upstream.Kind(strings.ToLower(scheme))); i >= 0 {
		a.s.Kind, a.s.Address = i, rest
	}
}

// loadApps reads the installed apps again, as they come and go.
func (a *app) loadApps() {
	go func() {
		list := a.sys.Apps()
		a.post(func() { a.apps = list })
	}()
}

// shown is the apps whose names match the query.
func (a *app) shown() []apps.App {
	q := strings.ToLower(strings.TrimSpace(a.query))
	if q == "" {
		return a.apps
	}
	var shown []apps.App
	for _, ap := range a.apps {
		if strings.Contains(strings.ToLower(ap.Name), q) {
			shown = append(shown, ap)
		}
	}
	return shown
}

// chosen is the installed apps the user checked.
func (a *app) chosen() []apps.App {
	var chosen []apps.App
	for _, ap := range a.apps {
		if a.isChosen(ap.Path) {
			chosen = append(chosen, ap)
		}
	}
	return chosen
}

func (a *app) isChosen(path string) bool { return slices.Contains(a.s.Apps, path) }

// choose checks an app or unchecks it; a running TUN follows at once.
func (a *app) choose(path string, on bool) {
	a.s.Apps = slices.DeleteFunc(a.s.Apps, func(p string) bool { return p == path })
	if on {
		a.s.Apps = append(a.s.Apps, path)
	}
	a.save()
	if a.on() && !a.cfg.All {
		a.cfg.Apps = a.chosenPaths()
		if err := a.sess.Update(a.cfg); err != nil {
			a.setNote(bad, "更新应用失败：%v", err)
		}
	}
}

func (a *app) chosenPaths() []string {
	var paths []string
	for _, ap := range a.chosen() {
		paths = append(paths, ap.Path)
	}
	return paths
}

func (a *app) on() bool { return a.sess != nil }

// turnOn starts the TUN, which asks for the password first.
func (a *app) turnOn() {
	if a.busy || a.on() {
		return
	}
	p, err := upstream.Parse(upstream.Kinds[a.s.Kind], a.s.Address)
	a.addrErr = ""
	if err != nil {
		a.addrErr = err.Error()
		return
	}
	cfg := tun.Config{Upstream: p.URL(), All: a.s.Mode == modeAll, Apps: a.chosenPaths(), Private: a.s.Private}
	if !cfg.All && len(cfg.Apps) == 0 {
		a.setNote(bad, "请先勾选要走代理的应用")
		return
	}
	a.save()
	a.busy = true
	a.setNote(plain, "")
	go func() {
		s, err := a.sys.StartTUN(cfg, func() { a.post(func() { a.installing = true }) })
		if err != nil {
			a.post(func() {
				a.busy, a.installing = false, false
				if errors.Is(err, tun.ErrCanceled) {
					a.setNote(plain, "已取消")
				} else {
					a.setNote(bad, "%v", err)
				}
			})
			return
		}
		a.post(func() {
			a.busy, a.installing, a.sess, a.cfg, a.via = false, false, s, cfg, p
			a.probing, a.latency, a.probeErr = true, 0, ""
		})
		// The events come in order, after the session is set.
		s.Wait(func(ev tun.Event) { a.post(func() { a.tunEvent(s, ev) }) })
	}()
}

func (a *app) tunEvent(s session, ev tun.Event) {
	if a.sess != s {
		return // turned off since
	}
	switch ev.Kind {
	case tun.EventProbe:
		a.probing, a.latency, a.probeErr = false, ev.Millis, ev.Error
	case tun.EventClosed:
		a.sess, a.probing = nil, false
		a.setNote(bad, "TUN 意外停止了，请重新开启")
	}
}

// turnOff takes the TUN down and gives the network back as it was.
func (a *app) turnOff() {
	if a.busy || !a.on() {
		return
	}
	s := a.sess
	a.sess, a.probing, a.busy = nil, false, true
	a.setNote(plain, "正在关闭…")
	go func() {
		s.Close()
		a.post(func() {
			a.busy = false
			a.setNote(plain, "")
		})
	}()
}

// uninstall removes the service, which asks for the password.
func (a *app) uninstall() {
	if a.busy || a.on() {
		return
	}
	a.busy = true
	a.setNote(plain, "正在卸载后台服务…")
	go func() {
		err := a.sys.UninstallService()
		a.post(func() {
			a.busy = false
			switch {
			case errors.Is(err, tun.ErrCanceled):
				a.setNote(plain, "已取消")
			case err != nil:
				a.setNote(bad, "卸载后台服务失败：%v", err)
			default:
				a.setNote(good, "已卸载后台服务，下次开启会重新安装")
			}
		})
	}()
}

// quit takes the TUN down before the app exits. It blocks, which the quit
// allows; the service would also take it down once the app is gone.
func (a *app) quit() {
	if a.on() {
		a.sess.Close()
		a.sess = nil
	}
}
