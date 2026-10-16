package tun

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/option"
	singjson "github.com/sagernet/sing/common/json"
)

// Every configuration loads in sing-box as it is, which is all but
// starting the TUN, which takes root.
func TestOptionsLoad(t *testing.T) {
	appsFile := filepath.Join(t.TempDir(), "apps.json")
	for _, apps := range [][]string{{"/Applications/Visual Studio Code.app"}, nil} {
		os.WriteFile(appsFile, appRules(apps), 0o600)
		for _, c := range []Config{
			{Upstream: "http://1.2.3.4:8080", All: true},
			{Upstream: "https://ada:secret@proxy.example.com:443", Private: true},
			{Upstream: "socks5://[::1]:1080", Apps: apps},
			{Upstream: "socks5://h:1080", All: true, Private: true},
		} {
			raw, err := options(c, appsFile)
			if err != nil {
				t.Fatal(err)
			}
			ctx := boxContext()
			opts, err := singjson.UnmarshalExtendedContext[option.Options](ctx, raw)
			if err != nil {
				t.Fatalf("%+v: %v", c, err)
			}
			b, err := box.New(box.Options{Context: ctx, Options: opts})
			if err != nil {
				t.Fatalf("%+v, apps %q: %v", c, apps, err)
			}
			b.Close()
		}
	}
}

func TestOptionsRoute(t *testing.T) {
	route := func(c Config) map[string]any {
		raw, err := options(c, "/x/apps.json")
		if err != nil {
			t.Fatal(err)
		}
		var o struct {
			Route     map[string]any   `json:"route"`
			Outbounds []map[string]any `json:"outbounds"`
		}
		json.Unmarshal(raw, &o)
		o.Route["proxy"] = o.Outbounds[0]
		return o.Route
	}

	all := route(Config{Upstream: "https://ada:secret@h:443", All: true})
	if all["final"] != "proxy" || all["rule_set"] != nil {
		t.Errorf("all apps: final %v, rule_set %v", all["final"], all["rule_set"])
	}
	p := all["proxy"].(map[string]any)
	if p["type"] != "http" || p["tls"] == nil || p["username"] != "ada" || p["password"] != "secret" || p["server_port"] != 443.0 {
		t.Errorf("HTTPS upstream: %v", p)
	}

	some := route(Config{Upstream: "socks5://h:1080", Apps: []string{"/Applications/A.app"}})
	if some["final"] != "direct" || some["rule_set"] == nil {
		t.Errorf("chosen apps: final %v, rule_set %v", some["final"], some["rule_set"])
	}
	if p := some["proxy"].(map[string]any); p["type"] != "socks" || p["version"] != "5" {
		t.Errorf("SOCKS5 upstream: %v", p)
	}

	// Private addresses go directly, unless they are to reach the network
	// behind the proxy.
	private := func(c Config) bool {
		for _, r := range route(c)["rules"].([]any) {
			if r.(map[string]any)["ip_is_private"] == true {
				return true
			}
		}
		return false
	}
	if !private(Config{Upstream: "socks5://h:1080", All: true}) {
		t.Error("private addresses through the proxy by default")
	}
	if private(Config{Upstream: "socks5://h:1080", All: true, Private: true}) {
		t.Error("private addresses directly, though asked through the proxy")
	}

	if _, err := options(Config{Upstream: "h"}, ""); err == nil {
		t.Error("no error for an upstream without a port")
	}
}

func TestAppRules(t *testing.T) {
	var rs struct {
		Rules []struct {
			Patterns []string `json:"process_path_regex"`
		} `json:"rules"`
	}
	json.Unmarshal(appRules([]string{"/Applications/Visual Studio Code.app"}), &rs)
	re := regexp.MustCompile(rs.Rules[0].Patterns[0])
	for path, want := range map[string]bool{
		"/Applications/Visual Studio Code.app/Contents/MacOS/Electron":                                        true,
		"/Applications/Visual Studio Code.app/Contents/Frameworks/Code Helper.app/Contents/MacOS/Code Helper": true,
		"/Applications/Visual Studio Code.app2/Contents/MacOS/Other":                                          false,
		"/Applications/Visual Studio CodeXapp/Contents/MacOS/Other":                                           false,
	} {
		if re.MatchString(path) != want {
			t.Errorf("%s matches %v, want %v", path, !want, want)
		}
	}
}
