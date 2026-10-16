package tun

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/option"
	singjson "github.com/sagernet/sing/common/json"
	M "github.com/sagernet/sing/common/metadata"
)

// engine runs the TUN; tests replace it, as only root can run sing-box's.
type engine interface {
	start(c Config) error
	setApps(apps []string) error
	probe() (time.Duration, error)
	close() error
}

// serve answers the app on conn: the first Config starts the TUN, the
// next ones change its apps, and the end of conn stops it.
func serve(conn io.ReadWriter, e engine) error {
	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)
	var mu sync.Mutex
	send := func(ev Event) {
		mu.Lock()
		defer mu.Unlock()
		enc.Encode(ev)
	}

	var c Config
	if err := dec.Decode(&c); err != nil {
		return err
	}
	defer e.close()
	if err := e.start(c); err != nil {
		send(Event{Kind: EventFailed, Error: err.Error()})
		return err
	}
	send(Event{Kind: EventStarted})
	go func() {
		d, err := e.probe()
		ev := Event{Kind: EventProbe, Millis: d.Milliseconds()}
		if err != nil {
			ev.Error = err.Error()
		}
		send(ev)
	}()
	for {
		if err := dec.Decode(&c); err != nil {
			return nil // the app is gone
		}
		if err := e.setApps(c.Apps); err != nil {
			return err
		}
	}
}

// singBox is the engine of sing-box.
type singBox struct {
	dir string // root's own, for the rule-set of the apps
	box *box.Box
}

func (s *singBox) start(c Config) error {
	dir, err := os.MkdirTemp("", "proxy-bridge-tun")
	if err != nil {
		return err
	}
	s.dir = dir
	if err := s.setApps(c.Apps); err != nil {
		return err
	}
	raw, err := options(c, s.appsFile())
	if err != nil {
		return err
	}
	ctx := boxContext()
	opts, err := singjson.UnmarshalExtendedContext[option.Options](ctx, raw)
	if err != nil {
		return err
	}
	b, err := box.New(box.Options{Context: ctx, Options: opts})
	if err != nil {
		return err
	}
	if err := b.Start(); err != nil {
		b.Close()
		return err
	}
	s.box = b
	return nil
}

func (s *singBox) appsFile() string { return filepath.Join(s.dir, "apps.json") }

// setApps rewrites the rule-set file, which sing-box reloads as it
// changes: the connections open stay as they are.
func (s *singBox) setApps(apps []string) error {
	tmp := s.appsFile() + ".tmp"
	if err := os.WriteFile(tmp, appRules(apps), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.appsFile())
}

// probe fetches ProbeURL through the upstream proxy, as the TUN does.
func (s *singBox) probe() (time.Duration, error) {
	proxy, _ := s.box.Outbound().Outbound("proxy")
	tr := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return proxy.DialContext(ctx, network, M.ParseSocksaddr(addr))
	}}
	defer tr.CloseIdleConnections()
	start := time.Now()
	resp, err := (&http.Client{Transport: tr, Timeout: 10 * time.Second}).Get(ProbeURL)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // without the method and the URL
		}
		return 0, err
	}
	resp.Body.Close()
	return time.Since(start), nil
}

func (s *singBox) close() error {
	var err error
	if s.box != nil {
		err = s.box.Close()
	}
	if s.dir != "" {
		os.RemoveAll(s.dir)
	}
	return err
}
