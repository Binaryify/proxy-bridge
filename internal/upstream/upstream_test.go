package upstream

import "testing"

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		kind Kind
		in   string
		want string // Proxy.String, or "" for an error
	}{
		{HTTP, "1.2.3.4:8080", "http://1.2.3.4:8080"},
		{SOCKS5, " proxy.example.com:1080 ", "socks5://proxy.example.com:1080"},
		{HTTPS, "proxy.example.com:443", "https://proxy.example.com:443"},
		{HTTP, "socks5://1.2.3.4:1080", "socks5://1.2.3.4:1080"}, // the scheme wins
		{HTTP, "HTTPS://h:443", "https://h:443"},
		{SOCKS5, "ada:secret@h:1080", "socks5://ada@h:1080"}, // no password shown
		{HTTP, "[::1]:8080", "http://[::1]:8080"},
		{HTTP, "", ""},
		{HTTP, "1.2.3.4", ""},
		{HTTP, "1.2.3.4:0", ""},
		{HTTP, "1.2.3.4:70000", ""},
		{HTTP, ":8080", ""},
		{HTTP, "ftp://h:21", ""},
	} {
		p, err := Parse(tc.kind, tc.in)
		if tc.want == "" {
			if err == nil {
				t.Errorf("Parse(%s, %q) = %s, want an error", tc.kind, tc.in, p)
			}
			continue
		}
		if err != nil || p.String() != tc.want {
			t.Errorf("Parse(%s, %q) = %s, %v; want %s", tc.kind, tc.in, p, err, tc.want)
		}
	}
	p, _ := Parse(SOCKS5, "ada:secret@h:1080")
	if pass, _ := p.User.Password(); pass != "secret" {
		t.Errorf("password %q", pass)
	}
}

func TestURLKeepsThePassword(t *testing.T) {
	p, _ := Parse(SOCKS5, "ada:p@ss@h:1080")
	q, err := Parse(HTTP, p.URL())
	if err != nil || q.String() != "socks5://ada@h:1080" {
		t.Fatalf("Parse(URL()) = %s, %v", q, err)
	}
	if pass, _ := q.User.Password(); pass != "p@ss" {
		t.Errorf("password %q", pass)
	}
}
