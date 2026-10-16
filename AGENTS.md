# AGENTS.md

用 TUN（精简编译的 sing-box）把全部应用或选中应用的流量送进 HTTP/HTTPS/SOCKS5 上游代理。Go + mygo 原生 UI（`github.com/egoist/mygo`，始终用最新版）。

- mygo 用法先看 `.agents/skills/mygo-maintenance/SKILL.md`，API 以 `go list -m -f '{{.Dir}}' github.com/egoist/mygo` 下的源码与 `docs/` 为准；sing-box 同理，以模块缓存里的源码和 `docs/` 为准。
- 验证：`go vet ./... && go tool mygo vet . && go test -race ./...`。界面改动跑 Snapshots 测试看截图（见 README）。
- 界面是菜单栏面板，风格与结构照 MonitorBar（dcchanInSz/MonitorBar）：配色和卡片、chip 在 `style.go`，面板自适应高度的逻辑在 `view.go`。测试里切换后要多画几帧（`fixture.frames`），滚动区一帧后才拿到内容高度。
- 线程：`app` 的字段只在 UI 线程读写；goroutine 的结果一律经 `app.post` 回来，过期事件（`a.sess != s`）丢弃。
- 系统副作用只走 `system` / `session` 接口（`main.go` 的 `desktop` 是真实实现）。测试和开发时不要真的以 root 启动 TUN。
- root 后台服务是本程序复制到 `/Library/PrivilegedHelperTools` 后以 `--service <uid> <app>` 由 launchd 常驻运行；应用连它的 socket（`internal/tun/session.go` 的 `Start`，缺失或版本不符时才走一次密码安装）。服务对每个连接核对用户和可执行文件路径；协议里不能有让 root 写任意路径或执行命令的字段。会话以连接断开为停止信号。root 脚本只用常量和 `$1 $2` 参数，改动后跑 `TestRootScripts`。
- `internal/tun/config.go` 的 `boxContext` 只注册用到的协议，加协议要同时注册，否则 sing-box 加载配置会失败（`TestOptionsLoad` 会发现）。
