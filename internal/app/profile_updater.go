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

const (
	maxAutoUpdateRetries = 3
	maxRetryCooldown     = 24 * time.Hour
)

type ProfileUpdater struct {
	cfg             *config.Manager
	st              *state.RuntimeState
	hub             *ProfileHub
	onActiveUpdated func(ctx context.Context, path string)

	isUpdating atomic.Bool
	failCounts map[string]int
}

func NewProfileUpdater(
	cfg *config.Manager,
	st *state.RuntimeState,
	hub *ProfileHub,
	onActiveUpdated func(ctx context.Context, path string),
) *ProfileUpdater {
	return &ProfileUpdater{
		cfg:             cfg,
		st:              st,
		hub:             hub,
		onActiveUpdated: onActiveUpdated,
		failCounts:      make(map[string]int),
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

			if !p.IsUpdateDue() {
				if p.NextUpdate == 0 {
					delete(u.failCounts, p.Path)
				}
				continue
			}

			if err := u.hub.UpdateRemote(ctx, p.Path); err == nil {
				delete(u.failCounts, p.Path)
				if p.Path == u.cfg.GetActivePath() {
					activeUpdatedPath = p.Path
				}
			} else if ctx.Err() == nil && !errors.Is(err, ErrProfileUpdating) && !errors.Is(err, config.ErrProfileNotFound) {
				u.failCounts[p.Path]++
				if u.failCounts[p.Path] >= maxAutoUpdateRetries {
					cooldown := time.Duration(p.Interval) * 24 * time.Hour
					if cooldown > maxRetryCooldown {
						cooldown = maxRetryCooldown
					}
					u.hub.DeferNextUpdate(p.Path, time.Now().Add(cooldown).Unix())
					delete(u.failCounts, p.Path)
					slog.Warn("自动更新连续失败，已延迟重试", "path", p.Path, "err", err)
				} else {
					slog.Warn("自动更新订阅失败", "path", p.Path, "err", err)
				}
			}

			timer := time.NewTimer(3 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}

		if activeUpdatedPath != "" && !u.st.IsExiting() && ctx.Err() == nil && u.onActiveUpdated != nil {
			if u.cfg.GetActivePath() == activeUpdatedPath {
				u.onActiveUpdated(ctx, activeUpdatedPath)
			}
		}
	}()
}
