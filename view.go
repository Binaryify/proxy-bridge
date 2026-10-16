package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"proxybridge/internal/upstream"
)

type tab struct{ name, icon string }

var tabs = []tab{{"概览", "gauge"}, {"应用列表", "grid"}}

const arrowHeight = 10

// panel is the menu bar panel showing the app's state, sized to its
// content.
type panel struct {
	a *app
	// arrowX is where the arrow points, from the panel's left edge.
	arrowX float32
	// maxContent caps the scrolling area, so the panel fits the screen.
	maxContent float32
	// onHeight hears the panel's natural height once it changes.
	onHeight func(h int)

	height        int
	contentHeight float32
	settle        int  // frames still to draw while the sizes catch up
	ownFrame      bool // this frame was asked for by settle
}

func (p *panel) view(c *ui.Context) {
	c.SetTheme(darkTheme)
	c.Root().Background(ui.Transparent) // the arrow and the rounded corners show the desktop
	shell := ui.Column(c).Width(panelWidth).TextColor(ink).FontSize(12)
	shell.Children(func() {
		p.arrow(c)
		ui.Column(c).PaddingX(13).Gradient(panelTop, panelBottom, 135).
			Radius(16).Border(0.7, ui.RGBA(255, 255, 255, 0.12)).Clip().Children(func() {
			p.header(c)
			p.tabs(c)
			p.content(c)
			p.footer(c)
		})
	})
	// Sizes come from the previous frame's layout. After a frame something
	// else asked for (input, a result), draw one more to measure it, and
	// more while the sizes keep changing.
	if !p.ownFrame {
		p.settle = max(p.settle, 1)
	}
	p.ownFrame = p.settle > 0
	if p.settle > 0 {
		p.settle--
		c.Invalidate()
	}
	if h := int(shell.Bounds().H + 0.5); h > 0 && h != p.height {
		p.height = h
		if p.onHeight != nil {
			go mygo.RunOnMain(func() { p.onHeight(h) }) // resize after this frame
		}
	}
}

func (p *panel) arrow(c *ui.Context) {
	x := min(max(p.arrowX, 18), panelWidth-18)
	ui.Box(c).Height(arrowHeight).Draw(func(pt *ui.Painter, r ui.Rect) {
		var path ui.Path
		left, tip, right := r.X+x-11, r.X+x, r.X+x+11
		path.MoveTo(left, r.Y+r.H)
		path.QuadTo(tip-3, r.Y+r.H-2, tip, r.Y)
		path.QuadTo(tip+3, r.Y+r.H-2, right, r.Y+r.H)
		path.Close()
		pt.FillPath(&path, panelTop)
	})
}

func (p *panel) header(c *ui.Context) {
	a := p.a
	ui.Row(c).Gap(10).Padding(16, 0, 14, 0).Children(func() {
		ui.Box(c).Size(32, 32).Radius(9).Center().Gradient(accent, accentDeep, 180).Children(func() {
			icon(c, "route").FontSize(15).TextColor(ui.RGB(255, 255, 255))
		})
		ui.Column(c).Gap(3).Grow(1).Children(func() {
			ui.Text(c, "代理桥").FontSize(16).FontWeight(600).LetterSpacing(-0.3)
			ui.Text(c, a.caption()).FontSize(10).FontWeight(500).TextColor(muted).SingleLine()
		})
		ui.Box(c).Size(24, 30).Center().TextColor(muted).Label("更多").Cursor(ui.CursorPointer).Children(func() {
			icon(c, "ellipsis").FontSize(16)
		}).Menu(func(m *ui.Menu) {
			if m.Item("重新读取应用列表").Chosen() {
				a.loadApps()
			}
			if a.sys.ServiceInstalled() {
				// While on, the TUN needs it.
				if m.Item("卸载后台服务…").Disabled(a.on() || a.busy).Chosen() {
					a.uninstall()
				}
			}
			m.Separator()
			if m.Item("退出代理桥").Chosen() {
				mygo.App.Quit()
			}
		})
		// The switch of the TUN: lit while on.
		label := "开启"
		if a.on() {
			label = "关闭"
		}
		power := ui.Box(c).Size(32, 32).Radius(16).Center().Cursor(ui.CursorPointer).
			Label(label).Tooltip(label + " TUN").Disabled(a.busy)
		switch {
		case a.on():
			power.Gradient(accent, accentDeep, 180).TextColor(ui.RGB(255, 255, 255))
		case power.Hovered():
			power.Background(ui.RGBA(255, 255, 255, 0.12))
		default:
			power.Background(ui.RGBA(255, 255, 255, 0.07))
		}
		power.Children(func() {
			if a.busy {
				ui.Spinner(c).Size(14, 14)
			} else {
				icon(c, "power").FontSize(15)
			}
		})
		power.OnClick(func() {
			if a.on() {
				a.turnOff()
			} else {
				a.turnOn()
			}
		})
	})
}

// caption sums the upstream up: "SOCKS5 · 1.2.3.4:1080".
func (a *app) caption() string {
	addr := strings.TrimSpace(a.s.Address)
	if addr == "" {
		return "还没有填写上游代理"
	}
	if _, host, ok := strings.Cut(addr, "@"); ok {
		addr = host // no password on screen
	}
	return strings.ToUpper(string(upstream.Kinds[a.s.Kind])) + " · " + addr
}

func (p *panel) tabs(c *ui.Context) {
	a := p.a
	ui.Row(c).Gap(4).Padding(4).Margin(0, 0, 12, 0).Radius(12).Background(ui.RGBA(255, 255, 255, 0.066)).Border(0.7, cardStroke).Children(func() {
		for i, t := range tabs {
			selected := a.s.Tab == i
			e := ui.Box(c.Key(t.name)).Grow(1).Height(34).Radius(9).Center().Tooltip(t.name).Label(t.name).Cursor(ui.CursorPointer)
			if selected {
				e.Background(accent.Alpha(0.2)).TextColor(accent)
			} else {
				e.TextColor(muted)
			}
			e.Children(func() { icon(c, t.icon).FontSize(16) })
			if e.Clicked() && !selected {
				a.s.Tab = i
				a.save()
				if i == 1 {
					a.loadApps()
				}
			}
		}
	})
}

// content scrolls the tab when it is taller than the screen allows.
func (p *panel) content(c *ui.Context) {
	scroll := ui.Scroll(c.Key("content")).Height(min(max(p.contentHeight, 1), p.maxContent))
	scroll.Children(func() {
		inner := ui.Column(c).Gap(12).Padding(0, 0, 4, 0)
		inner.Children(func() {
			if p.a.s.Tab == 1 {
				sectionTitle(c, tabs[1].name)
				p.apps(c)
			} else {
				p.overview(c)
			}
		})
		if h := inner.Bounds().H; h > 0 && h != p.contentHeight {
			p.contentHeight, p.settle = h, 3
			c.Invalidate()
		}
	})
}

// MARK: 概览

func (p *panel) overview(c *ui.Context) {
	a := p.a
	p.hero(c)
	// What the TUN runs with stays as it started until it is off.
	locked := a.on() || a.busy
	dim := func(e ui.Element) {
		if locked {
			e.Disabled(true).Opacity(0.55)
		}
	}
	dim(card(c, func() {
		cardTitle(c, "上游代理", "server", kindNote[a.s.Kind])
		ui.Grid(c).Columns(3).Gap(6).Children(func() {
			for i, k := range upstream.Kinds {
				label := strings.ToUpper(string(k))
				if chipText(c.Key(label), label, a.s.Kind == i) {
					a.s.Kind = i
				}
			}
		})
		ui.TextInput(c.Key("address"), &a.s.Address).Label("地址").
			Placeholder("1.2.3.4:1080 或 proxy.example.com:443").
			OnChange(a.addressChanged).
			OnSubmit(a.turnOn)
		if a.addrErr != "" {
			ui.Text(c, a.addrErr).FontSize(10).TextColor(red).Wrap()
		} else {
			ui.Text(c, "需要密码时写成 用户名:密码@主机:端口").FontSize(10).TextColor(muted)
		}
	}))
	dim(card(c, func() {
		trailing := ""
		if a.s.Mode == modeApps {
			trailing = fmt.Sprintf("已选 %d 个", len(a.chosen()))
		}
		cardTitle(c, "接管范围", "route", trailing)
		ui.Row(c).Gap(6).Children(func() {
			for _, m := range []struct {
				mode        int
				icon, label string
			}{{modeAll, "globe", "全部应用"}, {modeApps, "grid", "指定应用"}} {
				e := chip(c.Key(m.label), a.s.Mode == m.mode).Grow(1).Height(50).Gap(5).Label(m.label)
				e.Children(func() {
					icon(c, m.icon).FontSize(14)
					ui.Text(c, m.label).FontSize(10).FontWeight(500)
				})
				if e.Clicked() {
					a.s.Mode = m.mode
					a.save()
					if m.mode == modeApps {
						a.loadApps()
					}
				}
			}
		})
		ui.Text(c, modeHelp[a.s.Mode]).FontSize(10).TextColor(muted).Wrap()
		ui.Divider(c).Background(cardStroke)
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			// The text turns the switch over too, as a label does.
			ui.Column(c).Gap(2).Grow(1).Cursor(ui.CursorPointer).Children(func() {
				ui.Text(c, "内网地址走代理").FontSize(12).FontWeight(600)
				ui.Text(c, "10.x、172.16–31.x、192.168.x 等，用来访问代理那头的内网；本机所在网段始终直连").FontSize(10).TextColor(muted).Wrap()
			}).OnClick(func() {
				a.s.Private = !a.s.Private
				a.save()
			})
			if ui.Switch(c.Key("private"), &a.s.Private).Label("内网地址走代理").Changed() {
				a.save()
			}
		})
	}))
}

// hero draws the route the traffic takes, lit while the TUN is on, and
// how the upstream answers.
func (p *panel) hero(c *ui.Context) {
	a := p.a
	on := a.on()
	title, caption := "未开启", "开启后，流量经虚拟网卡送进上游代理"
	switch {
	case a.installing:
		title, caption = "安装后台服务", "请在系统对话框里输入管理员密码，只需要这一次"
	case a.busy && !on:
		title, caption = "开启中…", "正在启动虚拟网卡"
	case on && a.probing:
		title, caption = "测试中…", "正在经上游代理访问外网"
	case on && a.probeErr != "":
		title, caption = "上游不可用", a.probeErr
	case on:
		title, caption = fmt.Sprintf("%d ms", a.latency), "上游延迟 · "+a.via.String()
	}
	ui.Column(c).Height(170).AlignItems(ui.Center).Justify(ui.Center).Gap(16).Draw(func(pt *ui.Painter, r ui.Rect) {
		if on {
			// A shadow shows only outside its box: a hairline's is a soft halo.
			line := ui.Rect{X: r.X + r.W/2 - 70, Y: r.Y + r.H*0.4, W: 140, H: 0.01}
			pt.Shadow(line, 0.5, 0, 0, 70, 24, accent.Alpha(0.22))
		}
	}).Children(func() {
		ui.Row(c).AlignItems(ui.Start).Children(func() {
			node(c, "grid", "应用", on)
			link(c, on)
			node(c, "network", "TUN", on)
			link(c, on)
			node(c, "server", "代理", on)
		})
		ui.Column(c).Gap(4).AlignItems(ui.Center).PaddingX(16).Children(func() {
			ui.Text(c, title).FontSize(22).FontWeight(600).LetterSpacing(-0.4).FontFeatures("tnum")
			ui.Text(c, caption).FontSize(10).FontWeight(500).TextColor(muted).MaxLines(2).TextAlign(ui.Center)
		})
	})
}

// node is a stop of the route: a disc with an icon, named below it.
func node(c *ui.Context, iconName, label string, on bool) {
	ui.Column(c).Gap(6).AlignItems(ui.Center).Width(44).Children(func() {
		disc := ui.Box(c).Size(40, 40).Radius(20).Center()
		if on {
			disc.Background(accent.Alpha(0.18)).Border(0.8, accent.Alpha(0.55)).TextColor(accent)
		} else {
			disc.Background(ui.RGBA(255, 255, 255, 0.06)).Border(0.7, cardStroke).TextColor(muted)
		}
		disc.Children(func() { icon(c, iconName).FontSize(16) })
		ui.Text(c, label).FontSize(9).FontWeight(600).LetterSpacing(0.6).TextColor(muted)
	})
}

// link joins two nodes, level with their discs.
func link(c *ui.Context, on bool) {
	color := ui.RGBA(255, 255, 255, 0.12)
	if on {
		color = accent.Alpha(0.6)
	}
	ui.Box(c).Width(40).Height(2).Radius(1).Margin(19, 4, 0, 4).Background(color)
}

// MARK: 应用

func (p *panel) apps(c *ui.Context) {
	a := p.a
	if a.s.Mode == modeAll {
		card(c, func() {
			ui.Text(c, "现在接管的是全部应用。改为「指定应用」后，只有这里勾选的应用走代理。").FontSize(11).TextColor(muted).Wrap()
			if !a.on() && !a.busy && chipText(c.Key("to-apps"), "改为指定应用", false) {
				a.s.Mode = modeApps
				a.save()
			}
		})
	}
	chosen := a.chosen()
	ui.Row(c).Gap(8).Height(34).PaddingX(10).Radius(9).AlignItems(ui.Center).
		Background(ui.RGBA(255, 255, 255, 0.06)).Border(0.7, cardStroke).Children(func() {
		icon(c, "search").FontSize(13).TextColor(muted)
		ui.TextInputBase(c.Key("search"), &a.query).Label("搜索应用").Placeholder("搜索应用").Grow(1).FontSize(12)
		ui.Text(c, fmt.Sprintf("已选 %d", len(chosen))).FontSize(10).TextColor(muted)
	})

	shown := a.shown()
	a.appList.Key = func(i int) any { return shown[i].Path }
	ui.Column(c).Padding(4).Radius(12).Background(cardFill).Border(0.7, cardStroke).Children(func() {
		ui.List(c.Key("apps"), &a.appList, len(shown), func(i int) {
			ap := shown[i]
			selected := a.isChosen(ap.Path)
			row := ui.Row(c).Gap(8).Padding(8, 10).Radius(8).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(ap.Name)
			switch {
			case selected:
				row.Background(accent.Alpha(0.18))
			case row.Hovered():
				row.Background(ui.RGBA(255, 255, 255, 0.06))
			}
			row.Children(func() {
				name := ui.Text(c, ap.Name).FontSize(12).Grow(1).SingleLine()
				if selected {
					name.FontWeight(600)
					icon(c, "check").FontSize(12).TextColor(accent)
				}
			})
			row.OnClick(func() { a.choose(ap.Path, !selected) })
		}).Gap(2).Height(320).Children(func() {
			if len(shown) == 0 {
				empty := "没有匹配的应用"
				if a.apps == nil {
					empty = "正在读取应用…"
				}
				ui.Text(c, empty).TextColor(muted).Padding(12)
			}
		})
	})
	var names []string
	for _, ap := range chosen {
		names = append(names, ap.Name)
	}
	summary := "勾选的应用连同它的辅助进程走代理，开启后增减立即生效"
	if len(names) > 0 {
		summary = "已选：" + strings.Join(names, "、")
	}
	ui.Text(c, summary).FontSize(10).TextColor(muted).MaxLines(2).Padding(0, 2)
}

// MARK: footer

// footer tells how the TUN is, or how the last action went.
func (p *panel) footer(c *ui.Context) {
	a := p.a
	dot, text, spin := muted, "未开启", false
	switch {
	case a.installing:
		dot, text, spin = yellow, "首次使用：等待管理员密码以安装后台服务…", true
	case a.busy && !a.on() && a.note == "":
		dot, text, spin = yellow, "正在开启…", true
	case a.busy:
		dot, text, spin = yellow, a.note, true
	case a.note != "":
		dot, text = map[tone]ui.Color{plain: muted, good: green, bad: red}[a.noteTone], a.note
	case a.on():
		dot, text = green, "TUN 已开启 · 全部应用"
		if !a.cfg.All {
			text = fmt.Sprintf("TUN 已开启 · %d 个应用", len(a.cfg.Apps))
		}
	}
	ui.Row(c).Gap(6).Padding(12, 0).AlignItems(ui.Center).Children(func() {
		statusDot(c, dot)
		ui.Text(c, text).FontSize(10).TextColor(muted).Grow(1).Wrap()
		if spin {
			ui.Spinner(c).Size(12, 12)
		}
	})
}

// kindNote tells the kinds apart, by the index of upstream.Kinds.
var kindNote = []string{"普通 HTTP 代理", "TLS 加密连到代理", "域名交给代理解析"}

// modeHelp explains the modes, by their index.
var modeHelp = []string{
	modeAll:  "所有应用的流量都走代理，包括不认系统代理的应用",
	modeApps: "只有在「应用列表」里勾选的应用（连同它的辅助进程）走代理，其余直连",
}
