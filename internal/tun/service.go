package tun

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// The service: this executable, copied where only root can change it and
// run as root by launchd, at boot and whenever it ends.
const (
	Label      = "com.binaryify.proxybridge.helper"
	helperPath = "/Library/PrivilegedHelperTools/" + Label
	plistPath  = "/Library/LaunchDaemons/" + Label + ".plist"
	logPath    = "/Library/Logs/Proxy Bridge Helper.log"
)

// socketPath is where the service listens; tests move it.
var socketPath = "/var/run/" + Label + ".sock"

// RunService is the service: it serves the app at app, run by uid, one
// session at a time, until launchd stops it.
func RunService(uid int, app string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	version, err := fileHash(exe)
	if err != nil {
		return err
	}
	s, err := listenService(socketPath, uid, app, version, func() engine { return &singBox{} })
	if err != nil {
		return err
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signals
		s.Close()
	}()
	return s.serve()
}

// service accepts the app's sessions on a socket.
type service struct {
	ln        *net.UnixListener
	uid       int
	app       string
	version   string // the hash of the executable, which the app checks
	newEngine func() engine

	mu     sync.Mutex
	conn   net.Conn // the session served now
	closed bool
}

func listenService(sock string, uid int, app, version string, newEngine func() engine) (*service, error) {
	os.Remove(sock) // left by the last run
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		return nil, err
	}
	// Only uid reaches the socket, and serve checks each peer as well.
	if err := os.Chown(sock, uid, -1); err != nil {
		ln.Close()
		return nil, err
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return &service{ln: ln, uid: uid, app: app, version: version, newEngine: newEngine}, nil
}

// serve takes the sessions one after another, until Close.
func (s *service) serve() error {
	for {
		conn, err := s.ln.AcceptUnix()
		if err != nil {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.closed {
				return nil
			}
			return err
		}
		s.mu.Lock()
		s.conn = conn
		s.mu.Unlock()
		s.session(conn)
		s.mu.Lock()
		s.conn = nil
		s.mu.Unlock()
	}
}

// session answers one app: the TUN it starts goes down with the
// connection.
func (s *service) session(conn *net.UnixConn) {
	defer conn.Close()
	enc := json.NewEncoder(conn)
	if err := checkPeer(conn, s.uid, s.app); err != nil {
		enc.Encode(Event{Kind: EventRefused, Error: err.Error()})
		return
	}
	enc.Encode(Event{Kind: EventHello, Version: s.version})
	serve(conn, s.newEngine())
}

// Close stops listening and ends the session served now, which takes its
// TUN down.
func (s *service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.ln.Close()
	if s.conn != nil {
		s.conn.Close()
	}
}

// checkPeer lets in only the user the service was installed for, running
// the app it was installed from.
func checkPeer(conn *net.UnixConn, uid int, app string) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var cred *unix.Xucred
	var pid int
	var credErr, pidErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		pid, pidErr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	}); err != nil {
		return err
	}
	if credErr != nil || pidErr != nil {
		return errors.New("认不出连接的来源")
	}
	if int(cred.Uid) != uid {
		return errors.New("后台服务是为另一个用户安装的")
	}
	path, err := processPath(pid)
	if err != nil || !samePath(path, app) {
		return errors.New("后台服务是为另一个位置的代理桥安装的")
	}
	return nil
}

// processPath is the executable pid runs: kern.procargs2 holds argc, then
// the executable's path.
func processPath(pid int) (string, error) {
	b, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return "", err
	}
	if len(b) < 4 {
		return "", errors.New("procargs2 too short")
	}
	b = b[4:]
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b), nil
}

func samePath(a, b string) bool {
	resolve := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return filepath.Clean(p)
	}
	return resolve(a) == resolve(b)
}

// fileHash is the SHA-256 of the file, which tells versions apart.
func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
