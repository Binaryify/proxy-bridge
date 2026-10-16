package tun

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeEngine records what the service asks of it.
type fakeEngine struct {
	mu       sync.Mutex
	started  *Config
	apps     [][]string
	closed   bool
	startErr error
	appsErr  error
}

func (f *fakeEngine) start(c Config) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = &c
	return f.startErr
}

func (f *fakeEngine) setApps(apps []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.apps = append(f.apps, apps)
	return f.appsErr
}

func (f *fakeEngine) probe() (time.Duration, error) { return 42 * time.Millisecond, nil }

func (f *fakeEngine) close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeEngine) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// rig is a service run in the test, on a socket of its own, as the user
// running the test, for the test binary: what install sets up.
type rig struct {
	t       *testing.T
	exe     string
	version string
	svc     *service
	engines []*fakeEngine
	engine  func() *fakeEngine // makes each session's engine
	install int                // how many times install ran
}

func newRig(t *testing.T) *rig {
	exe, _ := os.Executable()
	version, err := fileHash(exe)
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, exe: exe, version: version, engine: func() *fakeEngine { return &fakeEngine{} }}
	// A short path: sockets take about a hundred bytes at most.
	dir, _ := os.MkdirTemp("", "pb")
	oldSocket, oldInstall := socketPath, install
	socketPath = filepath.Join(dir, "s.sock")
	install = func(exe string) error {
		r.install++
		r.run(os.Getuid(), exe, r.version)
		return nil
	}
	t.Cleanup(func() {
		if r.svc != nil {
			r.svc.Close()
		}
		socketPath, install = oldSocket, oldInstall
		os.RemoveAll(dir)
	})
	return r
}

// run (re)starts the service, as launchd would after an install.
func (r *rig) run(uid int, app, version string) {
	if r.svc != nil {
		r.svc.Close()
	}
	svc, err := listenService(socketPath, uid, app, version, func() engine {
		e := r.engine()
		r.engines = append(r.engines, e)
		return e
	})
	if err != nil {
		r.t.Fatal(err)
	}
	r.svc = svc
	go svc.serve()
}

func (r *rig) start(c Config) (*Session, int, error) {
	installing := 0
	s, err := Start(r.exe, r.version, c, func() { installing++ })
	return s, installing, err
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !ok(); {
		if time.Now().After(deadline) {
			t.Fatal("waited for ", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSession(t *testing.T) {
	r := newRig(t)
	r.run(os.Getuid(), r.exe, r.version)
	c := Config{Upstream: "socks5://h:1080", Apps: []string{"/Applications/A.app"}}
	s, installing, err := r.start(c)
	if err != nil {
		t.Fatal(err)
	}
	if installing != 0 || r.install != 0 {
		t.Errorf("installed a service that was in place")
	}
	e := r.engines[0]
	e.mu.Lock()
	if e.started == nil || e.started.Upstream != c.Upstream {
		t.Errorf("started with %+v", e.started)
	}
	e.mu.Unlock()

	events := make(chan Event, 8)
	go s.Wait(func(ev Event) { events <- ev })
	select {
	case ev := <-events:
		if ev.Kind != EventProbe || ev.Millis != 42 {
			t.Errorf("event %+v, want the probe", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no probe")
	}

	c.Apps = []string{"/Applications/A.app", "/Applications/B.app"}
	if err := s.Update(c); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the apps", func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return len(e.apps) == 1 && slices.Equal(e.apps[0], c.Apps)
	})

	// Close returns once the service has taken the TUN down, and reports
	// no unexpected end.
	s.Close()
	if !e.isClosed() {
		t.Error("the TUN is still up after Close")
	}
	select {
	case ev := <-events:
		t.Errorf("event %+v after Close", ev)
	default:
	}

	// The service takes the next session, with an engine of its own.
	s, _, err = r.start(c)
	if err != nil {
		t.Fatal(err)
	}
	go s.Wait(func(Event) {})
	s.Close()
	if len(r.engines) != 2 || !r.engines[1].isClosed() {
		t.Errorf("%d engines after two sessions", len(r.engines))
	}
}

// A service missing, out of date, or installed for another copy of the
// app is installed again, once.
func TestInstall(t *testing.T) {
	uid := os.Getuid()
	for name, setUp := range map[string]func(r *rig){
		"missing":      func(*rig) {},
		"out of date":  func(r *rig) { r.run(uid, r.exe, "old") },
		"another copy": func(r *rig) { r.run(uid, "/Applications/Elsewhere.app/Contents/MacOS/Proxy Bridge", r.version) },
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			setUp(r)
			s, installing, err := r.start(Config{All: true})
			if err != nil {
				t.Fatal(err)
			}
			go s.Wait(func(Event) {})
			s.Close()
			if installing != 1 || r.install != 1 {
				t.Errorf("installing %d, installed %d times", installing, r.install)
			}
		})
	}
}

// An app whose executable changed since it launched installs nothing: the
// new one is for the new app to install.
func TestUpdatedApp(t *testing.T) {
	r := newRig(t)
	_, err := Start(r.exe, "the version it launched as", Config{All: true}, func() { t.Error("installing") })
	if !errors.Is(err, ErrUpdated) || r.install != 0 {
		t.Errorf("Start = %v, installed %d times", err, r.install)
	}
}

func TestCanceled(t *testing.T) {
	r := newRig(t)
	install = func(string) error { return ErrCanceled }
	if _, _, err := r.start(Config{All: true}); !errors.Is(err, ErrCanceled) {
		t.Errorf("Start = %v after a canceled password", err)
	}
}

func TestStartFails(t *testing.T) {
	r := newRig(t)
	r.engine = func() *fakeEngine { return &fakeEngine{startErr: errors.New("no utun")} }
	r.run(os.Getuid(), r.exe, r.version)
	if _, _, err := r.start(Config{All: true}); err == nil || !strings.Contains(err.Error(), "no utun") {
		t.Errorf("Start = %v, want the engine's error", err)
	}
	waitFor(t, "the cleanup", r.engines[0].isClosed)
}

func TestSessionEnds(t *testing.T) {
	r := newRig(t)
	r.engine = func() *fakeEngine { return &fakeEngine{appsErr: errors.New("disk full")} }
	r.run(os.Getuid(), r.exe, r.version)
	s, _, err := r.start(Config{All: true})
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 8)
	go s.Wait(func(ev Event) { events <- ev })
	s.Update(Config{All: true})
	for ev := range events {
		if ev.Kind == EventClosed {
			break
		}
	}
	waitFor(t, "the cleanup", r.engines[0].isClosed)
}

func TestProcessPath(t *testing.T) {
	exe, _ := os.Executable()
	path, err := processPath(os.Getpid())
	if err != nil || !samePath(path, exe) {
		t.Errorf("processPath = %q, %v; want %q", path, err, exe)
	}
}

// What runs as root is checked as far as can be without running it.
func TestRootScripts(t *testing.T) {
	for name, script := range map[string]string{"install": installScript, "uninstall": uninstallScript} {
		if out, err := exec.Command("/bin/sh", "-n", "-c", script).CombinedOutput(); err != nil {
			t.Errorf("%s script: %v %s", name, err, out)
		}
	}

	plist := filepath.Join(t.TempDir(), "job.plist")
	os.WriteFile(plist, daemonPlist(501, "/Applications/A & B <x>.app/Contents/MacOS/A & B"), 0o644)
	if out, err := exec.Command("/usr/bin/plutil", "-lint", plist).CombinedOutput(); err != nil {
		t.Errorf("plist: %v %s", err, out)
	}
	out, _ := exec.Command("/usr/bin/plutil", "-extract", "ProgramArguments.3", "raw", "-o", "-", plist).Output()
	if got := strings.TrimSpace(string(out)); got != "/Applications/A & B <x>.app/Contents/MacOS/A & B" {
		t.Errorf("app path in the plist: %q", got)
	}

	// The AppleScript compiles; running it would ask for the password.
	var args []string
	for _, line := range adminScript {
		args = append(args, "-e", line)
	}
	compiled := filepath.Join(t.TempDir(), "admin.scpt")
	if out, err := exec.Command("/usr/bin/osacompile", append([]string{"-o", compiled}, args...)...).CombinedOutput(); err != nil {
		t.Errorf("AppleScript: %v %s", err, out)
	}
}

// checkPeer lets in the user and the copy of the app the service is for,
// and no other.
func TestCheckPeer(t *testing.T) {
	dir, _ := os.MkdirTemp("", "pb")
	defer os.RemoveAll(dir)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, "s"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	client, err := net.Dial("unix", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	conn, err := ln.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	exe, _ := os.Executable()
	uid := os.Getuid()
	if err := checkPeer(conn, uid, exe); err != nil {
		t.Errorf("refused its own user and app: %v", err)
	}
	if err := checkPeer(conn, uid+1, exe); err == nil {
		t.Error("let another user in")
	}
	if err := checkPeer(conn, uid, "/Applications/Elsewhere.app/Contents/MacOS/Proxy Bridge"); err == nil {
		t.Error("let another copy of the app in")
	}
}
