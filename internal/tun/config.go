// Package tun sends the traffic of every app, or of the apps chosen,
// through an upstream proxy, by a virtual network interface (TUN) that
// sing-box runs. Creating the interface takes root: a service, the app's
// own executable installed once as a launchd daemon, runs the TUN for the
// app while it stays connected.
package tun

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"regexp"
	"strconv"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter/certificate"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/adapter/outbound"
	boxservice "github.com/sagernet/sing-box/adapter/service"
	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/dns/transport/fakeip"
	"github.com/sagernet/sing-box/dns/transport/local"
	"github.com/sagernet/sing-box/protocol/block"
	"github.com/sagernet/sing-box/protocol/direct"
	"github.com/sagernet/sing-box/protocol/http"
	"github.com/sagernet/sing-box/protocol/socks"
	singtun "github.com/sagernet/sing-box/protocol/tun"

	"proxybridge/internal/upstream"
)

// Config is what the app asks of the service.
type Config struct {
	Upstream string   `json:"upstream"` // the upstream proxy as a URL, its password included
	All      bool     `json:"all"`      // every app's traffic, rather than Apps'
	Apps     []string `json:"apps"`     // the bundles whose processes go through the proxy
	// Private sends private addresses (10.x, 192.168.x…) through the proxy
	// as well, to reach the network behind it; else they go directly.
	// The subnets this Mac is on never enter the TUN either way.
	Private bool `json:"private"`
}

// Event is what the service tells the app.
type Event struct {
	Kind    string `json:"kind"`
	Error   string `json:"error,omitempty"`
	Millis  int64  `json:"millis,omitempty"`  // how long the probe took
	Version string `json:"version,omitempty"` // the service's, in its hello
}

// The kinds of events.
const (
	EventHello   = "hello"   // the service takes this session, as Version
	EventRefused = "refused" // it does not, for Error
	EventStarted = "started" // the TUN is up
	EventFailed  = "failed"  // it did not start, for Error
	EventProbe   = "probe"   // the upstream answered in Millis, or not, for Error
	EventClosed  = "closed"  // the service ended the session unasked
)

// ProbeURL answers 204 at once, from anywhere that reaches the internet.
const ProbeURL = "https://www.gstatic.com/generate_204"

// boxContext registers only what options uses, which keeps the other
// protocols of sing-box out of the executable.
func boxContext() context.Context {
	in := inbound.NewRegistry()
	singtun.RegisterInbound(in)
	out := outbound.NewRegistry()
	direct.RegisterOutbound(out)
	block.RegisterOutbound(out)
	http.RegisterOutbound(out)
	socks.RegisterOutbound(out)
	transports := dns.NewTransportRegistry()
	local.RegisterTransport(transports)
	fakeip.RegisterTransport(transports)
	return box.Context(context.Background(), in, out, endpoint.NewRegistry(), transports, boxservice.NewRegistry(), certificate.NewRegistry())
}

// options is the sing-box configuration for c:
//   - a TUN that takes every connection;
//   - fake-IP DNS, so that the proxy resolves the names apps connect to
//     and local DNS, polluted or not, does not matter;
//   - private addresses directly, as a system proxy leaves them, unless
//     c.Private;
//   - QUIC refused through the proxy, which HTTP proxies cannot carry,
//     so that browsers fall back to TCP;
//   - the apps in the rule-set file at appRuleSet, or all, through the
//     proxy, and the rest directly.
func options(c Config, appRuleSet string) ([]byte, error) {
	p, err := upstream.Parse(upstream.HTTP, c.Upstream)
	if err != nil {
		return nil, err
	}
	proxied := map[string]any{}
	route := map[string]any{
		"auto_detect_interface":   true,
		"find_process":            true,
		"default_domain_resolver": "local",
		"final":                   "direct",
	}
	if c.All {
		route["final"] = "proxy"
	} else {
		route["rule_set"] = []any{map[string]any{"type": "local", "tag": "apps", "format": "source", "path": appRuleSet}}
		proxied["rule_set"] = []string{"apps"}
	}
	quic := with(proxied, map[string]any{"network": []string{"udp"}, "port": []int{443}, "action": "reject"})
	rules := []any{
		map[string]any{"action": "sniff"},
		map[string]any{"protocol": "dns", "action": "hijack-dns"},
	}
	if !c.Private {
		rules = append(rules, map[string]any{"ip_is_private": true, "outbound": "direct"})
	}
	route["rules"] = append(rules, quic, with(proxied, map[string]any{"outbound": "proxy"}))
	return json.Marshal(map[string]any{
		"log": map[string]any{"level": "warn"},
		"dns": map[string]any{
			"servers": []any{
				map[string]any{"type": "local", "tag": "local"},
				map[string]any{"type": "fakeip", "tag": "fakeip", "inet4_range": "198.18.0.0/15", "inet6_range": "fc00::/18"},
			},
			"rules": []any{map[string]any{"query_type": []string{"A", "AAAA"}, "server": "fakeip"}},
			"final": "local",
		},
		"inbounds": []any{map[string]any{
			"type":       "tun",
			"tag":        "tun",
			"address":    []string{"172.19.0.1/30", "fdfe:dcba:9876::1/126"},
			"auto_route": true,
			"stack":      "system",
		}},
		"outbounds": []any{outboundOf(p), map[string]any{"type": "direct", "tag": "direct"}},
		"route":     route,
	})
}

// with is a rule made of the fields of both.
func with(a, b map[string]any) map[string]any {
	m := map[string]any{}
	for k, v := range a {
		m[k] = v
	}
	for k, v := range b {
		m[k] = v
	}
	return m
}

// outboundOf is the sing-box outbound reaching the upstream proxy.
func outboundOf(p upstream.Proxy) map[string]any {
	host, port, _ := net.SplitHostPort(p.Addr)
	n, _ := strconv.Atoi(port)
	o := map[string]any{"tag": "proxy", "type": "http", "server": host, "server_port": n}
	switch p.Kind {
	case upstream.SOCKS5:
		o["type"], o["version"] = "socks", "5"
	case upstream.HTTPS:
		o["tls"] = map[string]any{"enabled": true, "server_name": host}
	}
	if p.User != nil {
		pass, _ := p.User.Password()
		o["username"], o["password"] = p.User.Username(), pass
	}
	return o
}

// appRules is the rule-set file matching the processes run from the
// bundles, the app and its helpers.
func appRules(apps []string) []byte {
	var patterns []string
	for _, app := range apps {
		patterns = append(patterns, "^"+regexp.QuoteMeta(filepath.Clean(app)+"/"))
	}
	rules := []any{}
	if len(patterns) > 0 {
		rules = append(rules, map[string]any{"process_path_regex": patterns})
	}
	b, _ := json.Marshal(map[string]any{"version": 3, "rules": rules})
	return b
}
