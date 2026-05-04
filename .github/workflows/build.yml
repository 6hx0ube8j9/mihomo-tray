name: Build Mihomo Tray

on:
  push:
    branches: [ "main" ]
  workflow_dispatch:

jobs:
  build:
    runs-on: windows-latest
    env:
      GOOS: windows
      GOARCH: amd64
      FORCE_JAVASCRIPT_ACTIONS_TO_NODE24: true

    steps:
    - name: Checkout Code
      uses: actions/checkout@v4

    - name: Set up Go
      uses: actions/setup-go@v5
      with:
        go-version: '1.22'
        cache: false

    - name: Build
      shell: pwsh
      run: |
        if (Test-Path "go.mod") { Remove-Item "go.mod" }
        go mod init mihomo-tray
        go get golang.org/x/sys/windows
        go get github.com/getlantern/systray
        go get github.com/go-resty/resty/v2
        go mod tidy

        # 强制安装 winres 并指定输出
        go install github.com/tc-hib/go-winres@latest
        go-winres make
        
        # 确保 syso 文件存在于当前目录，Go build 才会链接它
        go build -ldflags "-s -w -H windowsgui" -o mihomo-tray.exe main.go

    - name: Upload
      uses: actions/upload-artifact@v4
      with:
        name: mihomo-tray
        path: mihomo-tray.exe
