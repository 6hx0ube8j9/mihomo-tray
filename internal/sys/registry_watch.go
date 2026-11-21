//go:build windows

package sys

import (
	"context"
	"runtime"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const registryWatchDebounceMs = 150

func watchRegistryKey(ctx context.Context, path string, onChange func()) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.NOTIFY|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	notifyEvent, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(notifyEvent)

	cancelEvent, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(cancelEvent)

	afterDone := make(chan struct{})
	stopCancel := context.AfterFunc(ctx, func() {
		defer close(afterDone)
		_ = windows.SetEvent(cancelEvent)
	})
	defer func() {
		if !stopCancel() {
			<-afterDone
		}
	}()

	arm := func() error {
		filter := uint32(windows.REG_NOTIFY_CHANGE_LAST_SET | windows.REG_NOTIFY_CHANGE_NAME | windows.REG_NOTIFY_THREAD_AGNOSTIC)
		err := windows.RegNotifyChangeKeyValue(windows.Handle(k), false, filter, notifyEvent, true)
		if err != nil {
			filter &^= windows.REG_NOTIFY_THREAD_AGNOSTIC
			err = windows.RegNotifyChangeKeyValue(windows.Handle(k), false, filter, notifyEvent, true)
		}
		return err
	}

	if err := arm(); err != nil {
		return err
	}

	handles := [2]windows.Handle{notifyEvent, cancelEvent}
	waitTimeout := uint32(windows.INFINITE)
	hasPendingUpdate := false

	for {
		event, err := windows.WaitForMultipleObjects(handles[:], false, waitTimeout)
		if err != nil {
			return err
		}
		if event == windows.WAIT_OBJECT_0+1 {
			return nil
		}

		switch event {
		case windows.WAIT_OBJECT_0:
			if err := arm(); err != nil {
				return err
			}
			waitTimeout = registryWatchDebounceMs
			hasPendingUpdate = true

		case uint32(windows.WAIT_TIMEOUT):
			waitTimeout = windows.INFINITE
			if hasPendingUpdate {
				hasPendingUpdate = false
				onChange()
			}
		}
	}
}