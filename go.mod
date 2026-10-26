module mihomo-tray

go 1.27.1

require (
	github.com/Microsoft/go-winio v0.6.2
	github.com/jchv/go-webview2 v0.0.0-20260205173254-56598839c808
	github.com/tailscale/walk v0.0.0-20260702185836-28b80ea70d3b
	github.com/tailscale/win v0.0.0-20260619195133-2d76c33a64c1
	golang.org/x/sys v0.48.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/dblohm7/wingoes v0.0.0-20231019175336-f6e33aa7cc34 // indirect
	github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1 // indirect
	golang.org/x/exp v0.0.0-20230425010034-47ecfdc1ba53 // indirect
	gopkg.in/Knetic/govaluate.v3 v3.0.0 // indirect
)

replace github.com/jchv/go-webview2 => github.com/6hx0ube8j9/go-webview2 v0.0.0-20261026072606-8733f5ef5ef5
