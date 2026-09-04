package sys

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"
	"log/slog"

	"golang.org/x/sys/windows"
)

func IsTunActive(targetDevice string) bool {
	target := strings.ToLower(strings.TrimSpace(targetDevice))
	if target == "" {
		return false
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		slog.Error("获取系统网卡列表失败", "err", err)
		return false
	}

	for _, i := range ifaces {
		name := strings.ToLower(i.Name)
		if strings.Contains(name, target) {
			return true
		}
	}

	return false
}

func WatchNetworkInterfaces(ctx context.Context, eventCh chan<- struct{}) {
	fd, err := windows.Socket(windows.AF_INET, windows.SOCK_DGRAM, windows.IPPROTO_UDP)
	if err != nil {
		slog.Error("创建网络监听 Socket 失败，降级为定时轮询模式", "err", err)
		fallbackWatch(ctx, eventCh)
		return
	}

	notifyCh := make(chan struct{}, 1)
	
	var closeOnce sync.Once
	safeCloseSocket := func() {
		closeOnce.Do(func() {
			_ = windows.Close(fd)
			slog.Debug("已释放网络监听 Socket")
		})
	}

	go func() {
		const SIO_ADDRESS_LIST_CHANGE = 0x28000017
		var bytesReturned uint32
		
		for {
			err := windows.WSAIoctl(fd, SIO_ADDRESS_LIST_CHANGE, nil, 0, nil, 0, &bytesReturned, nil, 0)
			if err != nil {
				slog.Debug("WSAIoctl 监听退出", "err", err)
				break
			}
			
			slog.Debug("底层硬件感知: 网络接口列表发生变化 (WSAIoctl)")
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
			for range notifyCh {}
			return

		case _, ok := <-notifyCh:
			if !ok {
				safeCloseSocket()
				return
			}
			if timer != nil {
				timer.Stop()
			}
			timer = time.NewTimer(100 * time.Millisecond)
			timerCh = timer.C

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

	ticker := time.NewTicker(2 * time.Second)
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
