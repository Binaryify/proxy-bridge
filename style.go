package main

import (
	"bytes"
	"image/png"

	"github.com/egoist/mygo/ui"
)

// The look of MonitorBar: a dark panel, faintly lit cards, orange accent.
const panelWidth = 360

var (
	accent      = ui.RGB(255, 105, 0)
	accentDeep  = ui.RGB(255, 69, 0)
	ink         = ui.RGBA(255, 255, 255, 0.87)
	muted       = ui.RGBA(255, 255, 255, 0.5)
	cardFill    = ui.RGBA(255, 255, 255, 0.065)
	cardStroke  = ui.RGBA(255, 255, 255, 0.08)
	panelTop    = ui.RGB(18, 19, 21)
	panelBottom = ui.RGB(10, 10, 10)
	green       = ui.RGB(52, 199, 89)
	yellow      = ui.RGB(255, 204, 0)
	red         = ui.RGB(255, 69, 58)
	darkTheme   = func() *ui.Theme {
		t := *ui.DarkTheme()
		t.Accent, t.AccentHover, t.AccentPressed = accent, accent, accentDeep
		return &t
	}()
)

func icon(c *ui.Context, name string) ui.Element { return ui.Icon(c, icons[name]) }

// card is the rounded, faintly lit box every group sits in.
func card(c *ui.Context, children func()) ui.Element {
	return ui.Column(c).Padding(12).Gap(10).Radius(12).Background(cardFill).Border(0.7, cardStroke).Children(children)
}

// chip is a pressable tile; selected ones take the accent.
func chip(c *ui.Context, selected bool) ui.Element {
	e := ui.Box(c).Radius(9).Center().Cursor(ui.CursorPointer)
	switch {
	case selected:
		e.Background(accent.Alpha(0.18)).Border(0.8, accent.Alpha(0.55)).TextColor(accent)
	case e.Pressed():
		e.Background(ui.RGBA(255, 255, 255, 0.13)).TextColor(ink)
	case e.Hovered():
		e.Background(ui.RGBA(255, 255, 255, 0.1)).TextColor(ink)
	default:
		e.Background(ui.RGBA(255, 255, 255, 0.06)).TextColor(ink)
	}
	return e
}

// chipText is a chip with one line of text.
func chipText(c *ui.Context, label string, selected bool) bool {
	e := chip(c, selected).Height(30).Label(label)
	e.Children(func() { ui.Text(c, label).FontSize(11).FontWeight(500).SingleLine() })
	return e.Clicked()
}

// cardTitle heads a card: an icon, a title, and a note at the end.
func cardTitle(c *ui.Context, title, iconName, trailing string) {
	ui.Row(c).Gap(6).Children(func() {
		icon(c, iconName).FontSize(13)
		ui.Text(c, title).FontSize(12).FontWeight(600).Grow(1)
		if trailing != "" {
			ui.Text(c, trailing).FontSize(10).TextColor(muted)
		}
	})
}

func sectionTitle(c *ui.Context, title string) {
	ui.Text(c, title).FontSize(11).Bold().LetterSpacing(0.8).TextColor(muted).Padding(0, 1)
}

// statusDot is the small colored dot before a status.
func statusDot(c *ui.Context, color ui.Color) {
	ui.Box(c).Size(6, 6).Radius(3).Background(color)
}

// Line icons in the style of Lucide, drawn in the text color.
var icons = map[string]*ui.SVG{}

func init() {
	for name, body := range map[string]string{
		"route":    `<circle cx="6" cy="19" r="3"/><path d="M9 19h8.5a3.5 3.5 0 0 0 0-7h-11a3.5 3.5 0 0 1 0-7H15"/><circle cx="18" cy="5" r="3"/>`,
		"gauge":    `<path d="m12 14 4-4"/><path d="M3.34 19a10 10 0 1 1 17.32 0"/>`,
		"grid":     `<rect width="7" height="7" x="3" y="3" rx="1"/><rect width="7" height="7" x="14" y="3" rx="1"/><rect width="7" height="7" x="14" y="14" rx="1"/><rect width="7" height="7" x="3" y="14" rx="1"/>`,
		"server":   `<rect width="20" height="8" x="2" y="2" rx="2"/><rect width="20" height="8" x="2" y="14" rx="2"/><path d="M6 6h.01"/><path d="M6 18h.01"/>`,
		"network":  `<rect x="16" y="16" width="6" height="6" rx="1"/><rect x="2" y="16" width="6" height="6" rx="1"/><rect x="9" y="2" width="6" height="6" rx="1"/><path d="M5 16v-3a1 1 0 0 1 1-1h12a1 1 0 0 1 1 1v3"/><path d="M12 12V8"/>`,
		"globe":    `<circle cx="12" cy="12" r="10"/><path d="M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20"/><path d="M2 12h20"/>`,
		"power":    `<path d="M12 2v10"/><path d="M18.4 6.6a9 9 0 1 1-12.77.04"/>`,
		"ellipsis": `<circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/><circle cx="5" cy="12" r="1"/>`,
		"check":    `<path d="M20 6 9 17l-5-5"/>`,
		"search":   `<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>`,
		"key":      `<path d="m15.5 7.5 2.3 2.3a1 1 0 0 0 1.4 0l2.1-2.1a1 1 0 0 0 0-1.4L19 4"/><path d="m21 2-9.6 9.6"/><circle cx="7.5" cy="15.5" r="5.5"/>`,
	} {
		icons[name] = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + body + `</svg>`))
	}
}

// trayIcon draws the route icon, black on transparent for macOS to tint:
// faint while off, as the menu bar shows what is inactive.
func trayIcon(on bool) []byte {
	alpha := float32(0.45)
	if on {
		alpha = 1
	}
	img := ui.Render(func(c *ui.Context) {
		c.Root().Background(ui.Transparent)
		ui.Box(c).Fill().Center().Children(func() {
			icon(c, "route").FontSize(15).TextColor(ui.RGBA(0, 0, 0, alpha))
		})
	}, 18, 18, 2)
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}
