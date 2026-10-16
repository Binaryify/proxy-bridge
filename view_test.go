package main

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"proxybridge/internal/apps"
	"proxybridge/internal/tun"
)

// fakeSystem starts fake sessions. Its methods run on the app's
// goroutines.
type fakeSystem struct {
	mu         sync.Mutex
	apps       []apps.App
	startErr   error
	started    []tun.Config
	sess       *fakeSession
	install    bool          // the service is missing: StartTUN installs it
	release    chan struct{} // when set, the install waits for it, as for the password
	installed  bool
	uninstalls int
}

func (f *fakeSystem) Apps() []apps.App { return f.apps }

func (f *fakeSystem) CheckForUpdates() {}

func (f *fakeSystem) ServiceInstalled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.installed
}

func (f *fakeSystem) UninstallService() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installed = false
	f.uninstalls++
	return nil
}

func (f *fakeSystem) StartTUN(c tun.Config, installing func()) (session, error) {
	if f.install {
		installing()
		if f.release != nil {
			<-f.release
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installed = true
	f.started = append(f.started, c)
	if f.startErr != nil {
		return nil, f.startErr
	}
	f.sess = &fakeSession{events: make(chan tun.Event, 8)}
	f.sess.events <- tun.Event{Kind: tun.EventProbe, Millis: 42}
	return f.sess, nil
}

// fakeSession is a TUN that reports what the test sends to events.
type fakeSession struct {
	mu      sync.Mutex
	events  chan tun.Event
	updates []tun.Config
	closed  bool
}

func (s *fakeSession) Update(c tun.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates = append(s.updates, c)
	return nil
}

func (s *fakeSession) Wait(onEvent func(tun.Event)) {
	for ev := range s.events {
		onEvent(ev)
	}
}

func (s *fakeSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.events)
	}
	return nil
}

type fixture struct {
	t     *testing.T
	a     *app
	p     *panel
	sys   *fakeSystem
	tt    *ui.Tester
	posts chan func()
	dir   string
}

func newFixture(t *testing.T) *fixture {
	f := &fixture{t: t, posts: make(chan func(), 16), dir: t.TempDir()}
	f.sys = &fakeSystem{apps: []apps.App{
		{Path: "/Applications/Code.app", Name: "Code"},
		{Path: "/Applications/IINA.app", Name: "IINA"},
		{Path: "/Applications/Slack.app", Name: "Slack"},
	}}
	f.a = newApp(f.dir, f.sys, func(fn func()) { f.posts <- fn })
	f.p = &panel{a: f.a, maxContent: 900, arrowX: panelWidth / 2}
	f.tt = ui.NewTester(f.p.view, panelWidth, 1100)
	f.frames()
	return f
}

// frames draws a few, as the scroll area takes its content's height a
// frame later.
func (f *fixture) frames() {
	for range 3 {
		f.tt.Frame()
	}
}

// settle runs n functions the app's goroutines post, as the window does,
// then draws a frame.
func (f *fixture) settle(n int) {
	f.t.Helper()
	for i := range n {
		select {
		case fn := <-f.posts:
			fn()
		case <-time.After(5 * time.Second):
			f.t.Fatalf("waited for post %d of %d", i+1, n)
		}
	}
	f.frames()
}

func (f *fixture) click(text string) {
	f.t.Helper()
	if err := f.tt.Click(text); err != nil {
		f.t.Fatalf("%v; texts %q", err, f.tt.Texts())
	}
	f.frames()
}

func (f *fixture) wantText(text string) {
	f.t.Helper()
	if !f.tt.HasText(text) {
		f.t.Errorf("no %q in %q", text, f.tt.Texts())
	}
}

func TestAllApps(t *testing.T) {
	f := newFixture(t)
	f.click("地址")
	// A pasted URL chooses its type.
	f.tt.Type("socks5://ada:secret@127.0.0.1:1080")
	f.tt.Frame()
	if f.a.s.Kind != 2 || f.a.s.Address != "ada:secret@127.0.0.1:1080" {
		t.Fatalf("kind %d, address %q", f.a.s.Kind, f.a.s.Address)
	}

	f.wantText("SOCKS5 · 127.0.0.1:1080") // no password on screen
	f.click("开启")
	f.wantText("正在启动虚拟网卡")
	f.settle(2) // on, then the probe
	f.wantText("TUN 已开启 · 全部应用")
	f.wantText("42 ms")
	f.wantText("上游延迟 · socks5://ada@127.0.0.1:1080")
	want := tun.Config{Upstream: "socks5://ada:secret@127.0.0.1:1080", All: true}
	if len(f.sys.started) != 1 || f.sys.started[0].Upstream != want.Upstream || !f.sys.started[0].All {
		t.Errorf("started %+v, want %+v", f.sys.started, want)
	}

	f.click("关闭")
	f.settle(1)
	f.wantText("未开启")
	f.wantText("开启")
	if !f.sys.sess.closed {
		t.Error("the TUN is still up")
	}

	// What was typed comes back at the next launch.
	if b := newApp(f.dir, f.sys, nil); b.s.Address != "ada:secret@127.0.0.1:1080" || b.s.Kind != 2 {
		t.Errorf("saved %+v", b.s)
	}
}

func TestBadAddress(t *testing.T) {
	f := newFixture(t)
	f.click("地址")
	f.tt.Type("1.2.3.4")
	f.click("开启")
	f.wantText("缺少端口或端口不在 1–65535，应为 主机:端口")
	if f.a.busy || len(f.sys.started) != 0 {
		t.Error("started with a bad address")
	}
}

func TestPasswordCanceled(t *testing.T) {
	f := newFixture(t)
	f.sys.startErr = tun.ErrCanceled
	f.a.s.Address = "127.0.0.1:8080"
	f.click("开启")
	f.settle(1)
	f.wantText("已取消")
	f.wantText("未开启")

	f.sys.startErr = errors.New("开启 TUN 失败：no utun")
	f.click("开启")
	f.settle(1)
	f.wantText("开启 TUN 失败：no utun")
}

func TestChosenApps(t *testing.T) {
	f := newFixture(t)
	f.a.s.Address = "127.0.0.1:8080"
	f.click("指定应用")
	f.settle(1) // the apps, read
	f.click("开启")
	f.wantText("请先勾选要走代理的应用")
	f.click("应用列表")
	f.settle(1) // read again as the tab shows
	f.wantText("IINA")

	f.click("Code")
	f.wantText("已选：Code")
	// The search filters the list, and keeps what is checked.
	f.click("搜索应用")
	f.tt.Type("sla")
	f.frames()
	if f.tt.HasText("IINA") || !f.tt.HasText("Slack") {
		t.Errorf("searching sla shows %q", f.tt.Texts())
	}
	f.wantText("已选：Code")

	f.click("开启")
	f.settle(2)
	if c := f.sys.started[0]; c.All || !slices.Equal(c.Apps, []string{"/Applications/Code.app"}) {
		t.Errorf("started %+v", c)
	}

	// Checking another app while on reaches the TUN at once.
	f.click("Slack")
	if u := f.sys.sess.updates; len(u) != 1 || !slices.Equal(u[0].Apps, []string{"/Applications/Code.app", "/Applications/Slack.app"}) {
		t.Errorf("updates %+v", u)
	}
	if b := newApp(f.dir, f.sys, nil); len(b.s.Apps) != 2 || b.s.Mode != modeApps {
		t.Errorf("saved %+v", b.s)
	}

	f.a.quit()
	if f.a.on() || !f.sys.sess.closed {
		t.Errorf("after quit: on %v, closed %v", f.a.on(), f.sys.sess.closed)
	}
}

func TestHelperEnds(t *testing.T) {
	f := newFixture(t)
	f.a.s.Address = "127.0.0.1:8080"
	f.click("开启")
	f.settle(2)
	f.sys.sess.events <- tun.Event{Kind: tun.EventClosed}
	f.settle(1)
	f.wantText("TUN 意外停止了，请重新开启")
	f.wantText("未开启")
}

// The first time, the service is installed, and the panel says why the
// password is asked for.
func TestFirstStartInstalls(t *testing.T) {
	f := newFixture(t)
	f.sys.install = true
	f.sys.release = make(chan struct{})
	f.a.s.Address = "127.0.0.1:8080"
	f.click("开启")
	f.settle(1) // installing
	f.wantText("安装后台服务")
	f.wantText("请在系统对话框里输入管理员密码，只需要这一次")
	f.wantText("首次使用：等待管理员密码以安装后台服务…")
	close(f.sys.release)
	f.settle(2)
	f.wantText("TUN 已开启 · 全部应用")
	if f.a.installing {
		t.Error("still installing once on")
	}
}

func TestUninstall(t *testing.T) {
	f := newFixture(t)
	f.sys.installed = true
	f.a.uninstall()
	f.settle(1)
	f.wantText("已卸载后台服务，下次开启会重新安装")
	if f.sys.uninstalls != 1 {
		t.Errorf("uninstalled %d times", f.sys.uninstalls)
	}

	// Not while the TUN needs it.
	f.a.s.Address = "127.0.0.1:8080"
	f.click("开启")
	f.settle(2)
	f.a.uninstall()
	if f.sys.uninstalls != 1 || f.a.busy {
		t.Error("uninstalled while on")
	}
}

func TestPrivateAddresses(t *testing.T) {
	f := newFixture(t)
	f.a.s.Address = "127.0.0.1:8080"
	f.click("内网地址走代理")
	if !f.a.s.Private {
		t.Fatal("the switch did not turn on")
	}
	f.click("开启")
	f.settle(2)
	if c := f.sys.started[0]; !c.Private {
		t.Errorf("started %+v, want private addresses through the proxy", c)
	}
	if b := newApp(f.dir, f.sys, nil); !b.s.Private {
		t.Error("the switch was not saved")
	}
}
