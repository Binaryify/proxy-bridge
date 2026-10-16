package tun

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"proxybridge/locales"
)

// Session is a running TUN, as the app sees it: the connection to the
// service, whose end stops it.
type Session struct {
	conn    *net.UnixConn
	dec     *json.Decoder
	mu      sync.Mutex // for writes
	closing atomic.Bool
	done    chan struct{} // closed once the service has let go
}

// ErrCanceled is the answer when the password dialog is canceled.
var ErrCanceled = errors.New("canceled")

// ErrUpdated is Start's answer when the app's executable changed since it
// launched: the running app would install a service it may not speak to.
var ErrUpdated = errors.New("the app was updated since it launched")

const (
	helloTimeout = 5 * time.Second  // for the service to answer
	readyTimeout = 15 * time.Second // for a service just installed to listen
	startTimeout = 30 * time.Second // for the TUN to come up
	closeTimeout = 5 * time.Second  // for the TUN to go down
)

// Version is the hash of exe, which the app takes as it launches and
// the service greets it with.
func Version(exe string) (string, error) { return fileHash(exe) }

// Start starts the TUN with c through the service, for the app running
// exe, whose Version was version as it launched. When the service is
// missing, out of date, or installed for another copy of the app, Start
// calls installing, then installs it from exe, which asks for the
// password; afterwards it needs none. It blocks.
func Start(exe, version string, c Config, installing func()) (*Session, error) {
	conn, dec, err := connect(version)
	if err != nil {
		if now, err := fileHash(exe); err != nil || now != version {
			return nil, ErrUpdated
		}
		installing()
		if err := install(exe); err != nil {
			return nil, err
		}
		for deadline := time.Now().Add(readyTimeout); ; time.Sleep(200 * time.Millisecond) {
			if conn, dec, err = connect(version); err == nil || time.Now().After(deadline) {
				break
			}
		}
		if err != nil {
			return nil, errors.New(locales.ErrServiceNotReady(err.Error()))
		}
	}
	s := &Session{conn: conn, dec: dec, done: make(chan struct{})}
	if err := s.Update(c); err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetReadDeadline(time.Now().Add(startTimeout))
	var ev Event
	if err := dec.Decode(&ev); err != nil {
		conn.Close()
		return nil, errors.New(locales.ErrServiceNoAnswer())
	}
	conn.SetReadDeadline(time.Time{})
	if ev.Kind != EventStarted {
		conn.Close()
		return nil, errors.New(locales.ErrStartFailed(ev.Error))
	}
	return s, nil
}

// connect reaches the service, which must greet this app as version.
func connect(version string) (*net.UnixConn, *json.Decoder, error) {
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return nil, nil, err
	}
	conn.SetReadDeadline(time.Now().Add(helloTimeout))
	dec := json.NewDecoder(conn)
	var ev Event
	err = dec.Decode(&ev)
	switch {
	case err != nil:
	case ev.Kind == EventRefused:
		err = errors.New(ev.Error)
	case ev.Kind != EventHello || ev.Version != version:
		err = errors.New("the service is another version")
	}
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	conn.SetReadDeadline(time.Time{})
	return conn, dec, nil
}

// Update sends c to the service, which takes its apps.
func (s *Session) Update(c Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	return json.NewEncoder(s.conn).Encode(c)
}

// Wait passes what the service reports to onEvent until the session
// ends: an EventClosed when it ended without Close.
func (s *Session) Wait(onEvent func(Event)) {
	defer close(s.done)
	for {
		var ev Event
		if err := s.dec.Decode(&ev); err != nil {
			if !s.closing.Load() {
				onEvent(Event{Kind: EventClosed})
			}
			return
		}
		onEvent(ev)
	}
}

// Close stops the TUN and waits a moment for the service to take it
// down, so that another can start at once. It needs Wait running.
func (s *Session) Close() error {
	if s.closing.Swap(true) {
		return nil
	}
	s.conn.CloseWrite() // the service reads its end and stops
	select {
	case <-s.done:
	case <-time.After(closeTimeout):
	}
	return s.conn.Close()
}

// Installed tells whether the service is installed.
func Installed() bool {
	_, err := os.Stat(plistPath)
	return err == nil
}

// install puts exe in place as the service, for this user and this copy
// of the app, and starts it; the system asks for the password. Tests
// replace it.
var install = func(exe string) error {
	if os.Getuid() == 0 {
		return errors.New("Proxy Bridge must not run as root")
	}
	plist, err := os.CreateTemp("", "proxy-bridge-*.plist")
	if err != nil {
		return err
	}
	defer os.Remove(plist.Name())
	_, err = plist.Write(daemonPlist(os.Getuid(), exe))
	if cerr := plist.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return admin(locales.PromptInstall(), installScript, exe, plist.Name())
}

// Uninstall stops the service and removes it; the system asks for the
// password.
func Uninstall() error {
	return admin(locales.PromptUninstall(), uninstallScript)
}

// The scripts run as root, with their arguments as $1, $2: they take no
// text from elsewhere.
var (
	installScript = fmt.Sprintf(`set -e
launchctl bootout system/%[1]s 2>/dev/null || true
mkdir -p /Library/PrivilegedHelperTools
cp "$1" %[2]q.new
chown root:wheel %[2]q.new
chmod 755 %[2]q.new
mv -f %[2]q.new %[2]q
cp "$2" %[3]q
chown root:wheel %[3]q
chmod 644 %[3]q
launchctl bootstrap system %[3]q || { sleep 1; launchctl bootstrap system %[3]q; }
`, Label, helperPath, plistPath)
	uninstallScript = fmt.Sprintf(`launchctl bootout system/%[1]s 2>/dev/null || true
rm -f %[2]q %[3]q %[4]q
`, Label, helperPath, plistPath, socketPath)
)

// adminScript runs a shell script as root through the system's password
// dialog: its arguments are the prompt, the script and the script's own.
var adminScript = []string{
	"on run argv",
	`set cmd to "/bin/sh -c " & quoted form of item 2 of argv & " sh"`,
	"repeat with i from 3 to count of argv",
	`set cmd to cmd & " " & quoted form of item i of argv`,
	"end repeat",
	"do shell script cmd with prompt (item 1 of argv) with administrator privileges",
	"end run",
}

// admin runs script as root, showing prompt in the password dialog.
func admin(prompt, script string, args ...string) error {
	var cmd []string
	for _, l := range adminScript {
		cmd = append(cmd, "-e", l)
	}
	cmd = append(cmd, prompt, script)
	out, err := exec.Command("/usr/bin/osascript", append(cmd, args...)...).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "-128") {
			return ErrCanceled
		}
		return errors.New(locales.ErrAdminFailed(strings.TrimSpace(string(out))))
	}
	return nil
}

// daemonPlist is the launchd job of the service, for uid and the app at
// app: started at boot and whenever it ends, logging its errors.
func daemonPlist(uid int, app string) []byte {
	str := func(s string) string {
		var b bytes.Buffer
		xml.EscapeText(&b, []byte(s))
		return "<string>" + b.String() + "</string>"
	}
	var args string
	for _, a := range []string{helperPath, "--service", strconv.Itoa(uid), app} {
		args += "\n\t\t" + str(a)
	}
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	` + str(Label) + `
	<key>ProgramArguments</key>
	<array>` + args + `
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardErrorPath</key>
	` + str(logPath) + `
</dict>
</plist>
`)
}
