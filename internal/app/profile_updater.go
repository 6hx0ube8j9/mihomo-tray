package app

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/state"
)

type ProfileUpdater struct {
	cfg       *config.Manager
	st        *state.RuntimeState
	hub       *ProfileHub
	onUpdated func()

	isUpdating atomic.Bool
}

func NewProfileUpdater(cfg *config.Manager, st *state.RuntimeState, hub *ProfileHub, onUpdated func()) *ProfileUpdater {
	return &ProfileUpdater{
		cfg:       cfg,
		st:        st,
		hub:       hub,
		onUpdated: onUpdated,
	}
}

func (u *ProfileUpdater) Trigger(ctx context.Context) {
	if !u.isUpdating.CompareAndSwap(false, true) {
		return
	}

	go func() {
		defer u.isUpdating.Store(false)
		hasUpdate := false

		for _, p := range u.cfg.GetProfiles() {
			if u.st.IsExiting() || ctx.Err() != nil {
				return
			}

			if p.IsUpdateDue() {
				if err := u.hub.UpdateRemote(ctx, p.Path); err == nil {
					hasUpdate = true
				} else if !errors.Is(err, ErrProfileUpdating) {
					slog.Warn("自动更新订阅失败", "path", p.Path, "err", err)
				}

				select {
				case <-ctx.Done():
					return
				case <-time.After(3 * time.Second):
				}
			}
		}

		if hasUpdate && u.onUpdated != nil {
			u.onUpdated()
		}
	}()
}
