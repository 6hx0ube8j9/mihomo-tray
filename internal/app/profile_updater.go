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
	cfg             *config.Manager
	st              *state.RuntimeState
	hub             *ProfileHub
	onUpdated       func()
	onActiveUpdated func(ctx context.Context, path string)

	isUpdating atomic.Bool
}

func NewProfileUpdater(
	cfg *config.Manager,
	st *state.RuntimeState,
	hub *ProfileHub,
	onUpdated func(),
	onActiveUpdated func(ctx context.Context, path string),
) *ProfileUpdater {
	return &ProfileUpdater{
		cfg:             cfg,
		st:              st,
		hub:             hub,
		onUpdated:       onUpdated,
		onActiveUpdated: onActiveUpdated,
	}
}

func (u *ProfileUpdater) Trigger(ctx context.Context) {
	if !u.isUpdating.CompareAndSwap(false, true) {
		return
	}

	go func() {
		defer u.isUpdating.Store(false)
		activeUpdatedPath := ""

		for _, p := range u.cfg.GetProfiles() {
			if u.st.IsExiting() || ctx.Err() != nil {
				return
			}

			if p.IsUpdateDue() {
				if err := u.hub.UpdateRemote(ctx, p.Path); err == nil {
					if u.onUpdated != nil {
						u.onUpdated()
					}
					if p.Path == u.cfg.GetActivePath() {
						activeUpdatedPath = p.Path
					}
				} else if ctx.Err() == nil && !errors.Is(err, ErrProfileUpdating) && !errors.Is(err, config.ErrProfileNotFound) {
					slog.Warn("自动更新订阅失败", "path", p.Path, "err", err)
				}

				timer := time.NewTimer(3 * time.Second)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}

		if activeUpdatedPath != "" && !u.st.IsExiting() && ctx.Err() == nil && u.onActiveUpdated != nil {
			if u.cfg.GetActivePath() == activeUpdatedPath {
				u.onActiveUpdated(ctx, activeUpdatedPath)
			}
		}
	}()
}
