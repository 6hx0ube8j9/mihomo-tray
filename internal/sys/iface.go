//go:build windows

package sys

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

const (
	networkDebounceInterval = 250 * time.Millisecond
	fallbackPollInterval    = 2 * time.Second
)

func IsTunActive(targetDevice string) bool {
	target := strings.ToLower(strings.TrimSpace(targetDevice))
	if target == "" {
		return false
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		slog.Warn("获取系统网卡列表失败", "err", err)
		return false
	}

	for _, i := range ifaces {
		name := strings.ToLower(i.Name)
		if name == target || strings.HasPrefix(name, target+" ") {
			if i.Flags&net.FlagUp != 0 {
				return true
			}
		}
	}

	return false
}

func WatchNetworkInterfaces(ctx context.Context, eventCh chan<- struct{}) {
	fd, err := windows.Socket(windows.AF_INET, windows.SOCK_DGRAM, windows.IPPROTO_UDP)
	if err != nil {
		slog.Warn("创建网络监听失败，改用定时轮询", "err", err)
		fallbackWatch(ctx, eventCh)
		return
	}

	select {
	case eventCh <- struct{}{}:
	default:
	}

	notifyCh := make(chan struct{}, 1)

	var closeOnce sync.Once
	safeCloseSocket := func() {
		closeOnce.Do(func() {
			_ = windows.Closesocket(fd)
		})
	}

	go func() {
		const sioAddressListChange = 0x28000017
		var bytesReturned uint32

		for {
			if err := windows.WSAIoctl(fd, sioAddressListChange, nil, 0, nil, 0, &bytesReturned, nil, 0); err != nil {
				if ctx.Err() == nil {
					slog.Debug("网络监听退出", "err", err)
				}
				break
			}

			select {
			case notifyCh <- struct{}{}:
			default:
			}
		}
		close(notifyCh)
	}()

	var timer *time.Timer
	var timerCh <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			safeCloseSocket()
			return

		case _, ok := <-notifyCh:
			if !ok {
				if timer != nil {
					timer.Stop()
				}
				safeCloseSocket()
				if ctx.Err() == nil {
					slog.Warn("网络监听中断，改用定时轮询")
					fallbackWatch(ctx, eventCh)
				}
				return
			}

			if timer == nil {
				timer = time.NewTimer(networkDebounceInterval)
				timerCh = timer.C
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(networkDebounceInterval)
			}

		case <-timerCh:
			timerCh = nil
			timer = nil

			select {
			case <-ctx.Done():
				safeCloseSocket()
				return
			case eventCh <- struct{}{}:
			default:
			}
		}
	}
}

func fallbackWatch(ctx context.Context, eventCh chan<- struct{}) {
	select {
	case eventCh <- struct{}{}:
	default:
	}

	ticker := time.NewTicker(fallbackPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			select {
			case eventCh <- struct{}{}:
			default:
			}
		}
	}
}