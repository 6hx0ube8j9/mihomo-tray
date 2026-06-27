package sys

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

var tunKeywords = []string{"mihomo", "meta", "clash", "wintun"}

func IsTunActive(targetDevice string) bool {
	target := strings.ToLower(strings.TrimSpace(targetDevice))
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}

	for _, i := range ifaces {
		name := strings.ToLower(i.Name)
	
		if target != "" && strings.Contains(name, target) {
			return true
		}
		
		for _, kw := range tunKeywords {
			if strings.Contains(name, kw) {
				return true
			}
		}
	}

	return false
}

func WatchNetworkInterfaces(ctx context.Context, eventCh chan<- struct{}) {
	fd, err := windows.Socket(windows.AF_INET, windows.SOCK_DGRAM, windows.IPPROTO_UDP)
	if err != nil {
		fallbackWatch(ctx, eventCh)
		return
	}

	notifyCh := make(chan struct{}, 1)
	
	var closeOnce sync.Once
	safeCloseSocket := func() {
		closeOnce.Do(func() {
			_ = windows.Close(fd)
		})
	}

	go func() {
		const SIO_ADDRESS_LIST_CHANGE = 0x28000017
		var bytesReturned uint32
		
		for {
			err := windows.WSAIoctl(fd, SIO_ADDRESS_LIST_CHANGE, nil, 0, nil, 0, &bytesReturned, nil, 0)
			if err != nil {
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
