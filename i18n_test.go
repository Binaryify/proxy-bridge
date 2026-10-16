package main

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/i18n"
)

// The tests show the panel in the language they choose, Simplified
// Chinese unless they say otherwise, whatever the system's.
var testLocale atomic.Value

func init() {
	testLocale.Store("zh-Hans")
	i18n.SetLocaleFunc(func() string { return testLocale.Load().(string) })
}

func inLocale(t *testing.T, tag string) {
	testLocale.Store(tag)
	t.Cleanup(func() { testLocale.Store("zh-Hans") })
}

func TestEnglish(t *testing.T) {
	inLocale(t, "en")
	f := newFixture(t)
	f.wantText("Proxy Bridge")
	f.wantText("No upstream proxy yet")
	f.wantText("Upstream Proxy")
	f.wantText("All Apps")
	f.wantText("Private Addresses via Proxy")

	f.click("Address")
	f.tt.Type("1.2.3.4")
	f.click("Turn On")
	f.wantText("Missing port, or not within 1–65535; the address should be host:port")

	f.a.s.Address = "127.0.0.1:1080"
	f.click("Chosen Apps")
	f.settle(1)
	f.click("Turn On")
	f.wantText("Check the apps to send through the proxy first")
	f.click("Apps")
	f.settle(1)
	f.click("Code")
	f.click("Slack")
	f.wantText("Chosen: Code, Slack")
	f.click("Turn On")
	f.settle(2)
	f.wantText("TUN on · 2 apps")
	f.wantText("Turn Off")
}

// Every message of the source has its translation, and no more.
func TestCatalogs(t *testing.T) {
	keys := func(path string) []string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		json.Unmarshal(b, &m)
		var ks []string
		for k := range m {
			if !strings.HasPrefix(k, "@") {
				ks = append(ks, k)
			}
		}
		slices.Sort(ks)
		return ks
	}
	if en, zh := keys("locales/en.json"), keys("locales/zh-Hans.json"); !slices.Equal(en, zh) {
		t.Errorf("the catalogs differ:\nen      %q\nzh-Hans %q", en, zh)
	}
}

// The … menu chooses the language, which shows at once and is kept.
func TestChooseLanguage(t *testing.T) {
	t.Cleanup(func() { testLocale.Store("zh-Hans") })
	f := newFixture(t)
	f.click("更多")
	if err := f.tt.ChooseMenuItem("语言", "English"); err != nil {
		t.Fatalf("%v; menu %v", err, f.tt.Menu())
	}
	f.frames()
	f.wantText("Upstream Proxy")
	if f.a.s.Language != "en" || f.sys.language == nil || *f.sys.language != "en" {
		t.Errorf("language %q", f.a.s.Language)
	}
	if b := newApp(f.dir, f.sys, nil); b.s.Language != "en" {
		t.Error("the choice was not saved")
	}

	f.click("More")
	if err := f.tt.ChooseMenuItem("Language", "Follow System"); err != nil {
		t.Fatalf("%v; menu %v", err, f.tt.Menu())
	}
	f.frames()
	f.wantText("上游代理")
	if f.a.s.Language != "" {
		t.Errorf("language %q, want the system's", f.a.s.Language)
	}
}
