// Package upstream reads the proxy the user types.
package upstream

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Kind is the protocol spoken to the proxy.
type Kind string

const (
	HTTP   Kind = "http"  // CONNECT over TCP
	HTTPS  Kind = "https" // CONNECT over TLS to the proxy
	SOCKS5 Kind = "socks5"
)

// Kinds lists every kind, in the order the window shows them.
var Kinds = []Kind{HTTP, HTTPS, SOCKS5}

// Proxy is a parsed upstream proxy.
type Proxy struct {
	Kind Kind
	Addr string        // host:port
	User *url.Userinfo // nil when the proxy needs no password
}

// Parse reads "host:port" or "user:pass@host:port" as a proxy of kind.
// A scheme in front, as "socks5://host:port", wins over kind.
func Parse(kind Kind, s string) (Proxy, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Proxy{}, errors.New("请填写代理地址，例如 1.2.3.4:1080")
	}
	if !strings.Contains(s, "://") {
		s = string(kind) + "://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return Proxy{}, errors.New("地址格式不对，应为 主机:端口")
	}
	p := Proxy{Kind: Kind(strings.ToLower(u.Scheme)), Addr: u.Host, User: u.User}
	if !p.Kind.valid() {
		return Proxy{}, fmt.Errorf("不支持 %s，只支持 http、https、socks5", u.Scheme)
	}
	if u.Hostname() == "" {
		return Proxy{}, errors.New("缺少主机，应为 主机:端口")
	}
	if port, err := strconv.Atoi(u.Port()); err != nil || port < 1 || port > 65535 {
		return Proxy{}, errors.New("缺少端口或端口不在 1–65535，应为 主机:端口")
	}
	return p, nil
}

func (k Kind) valid() bool {
	for _, v := range Kinds {
		if k == v {
			return true
		}
	}
	return false
}

// String is the proxy as a URL, without its password.
func (p Proxy) String() string {
	s := string(p.Kind) + "://"
	if p.User != nil {
		s += p.User.Username() + "@"
	}
	return s + p.Addr
}

// URL is the proxy as a URL, its password included.
func (p Proxy) URL() string {
	return (&url.URL{Scheme: string(p.Kind), User: p.User, Host: p.Addr}).String()
}
