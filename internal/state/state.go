package state

import (
	"sync"
	"sync/atomic"
	"time"

	"mihomo-tray/internal/domain"
)

const (
	TunInitGracePeriod = 20 * time.Second
	TunLostAlarmDelay  = 6 * time.Second
)

type KernelAction int32

const (
	ActionNone KernelAction = iota
	ActionSwitchProfile
	ActionReload
	ActionRestart
	ActionSyncAPI
)

func (a KernelAction) String() string {
	switch a {
	case ActionSwitchProfile:
		return "switch_profile"
	case ActionReload:
		return "reload"
	case ActionRestart:
		return "restart"
	case ActionSyncAPI:
		return "sync_api"
	default:
		return "none"
	}
}

type RuntimeState struct {
	phase atomic.Int32

	actionMu       sync.RWMutex
	currentAction  KernelAction
	pendingProfile string

	updatingProfiles atomic.Bool

	tunAlive    atomic.Bool
	tunReqTime  atomic.Int64
	tunLostTime atomic.Int64

	tunDevName  atomic.Value
	systemTheme atomic.Value
}

func NewRuntimeState() *RuntimeState {
	rs := &RuntimeState{}
	rs.phase.Store(int32(domain.PhaseInitializing))
	rs.systemTheme.Store(domain.ThemeLight)
	return rs
}

// ---------------- 核心排他动作状态机 ----------------

func (r *RuntimeState) IsActionBusy() bool {
	return r.CurrentAction() != ActionNone
}

func (r *RuntimeState) TryBeginAction(action KernelAction) bool {
	return r.TryBeginProfileAction(action, "")
}

func (r *RuntimeState) TryBeginProfileAction(action KernelAction, pendingPath string) bool {
	if r.IsExiting() || action == ActionNone {
		return false
	}

	r.actionMu.Lock()
	defer r.actionMu.Unlock()

	if r.currentAction != ActionNone || r.IsExiting() {
		return false
	}
	r.currentAction = action
	r.pendingProfile = pendingPath
	return true
}

func (r *RuntimeState) EndAction() {
	r.actionMu.Lock()
	r.pendingProfile = ""
	r.currentAction = ActionNone
	r.actionMu.Unlock()
}

func (r *RuntimeState) CurrentAction() KernelAction {
	r.actionMu.RLock()
	action := r.currentAction
	r.actionMu.RUnlock()
	return action
}

func (r *RuntimeState) ActionVisualState() (isBusy bool, pendingProfile string) {
	r.actionMu.RLock()
	isBusy = r.currentAction != ActionNone
	pendingProfile = r.pendingProfile
	r.actionMu.RUnlock()
	return isBusy, pendingProfile
}

// ---------------- 订阅批量更新状态 ----------------

func (r *RuntimeState) TryBeginProfileBatchUpdate() bool {
	if r.IsExiting() {
		return false
	}
	return r.updatingProfiles.CompareAndSwap(false, true)
}

func (r *RuntimeState) EndProfileBatchUpdate() {
	r.updatingProfiles.Store(false)
}

func (r *RuntimeState) IsProfileBatchUpdating() bool {
	return r.updatingProfiles.Load()
}

// ---------------- 生命周期与阶段控制 ----------------

func (r *RuntimeState) GetPhase() domain.AppPhase {
	return domain.AppPhase(r.phase.Load())
}

func (r *RuntimeState) SetPhase(p domain.AppPhase) {
	for {
		curr := r.phase.Load()
		if domain.AppPhase(curr) == domain.PhaseExiting {
			return
		}
		if r.phase.CompareAndSwap(curr, int32(p)) {
			return
		}
	}
}

func (r *RuntimeState) ForceExitPhase() {
	r.phase.Store(int32(domain.PhaseExiting))
}

func (r *RuntimeState) IsExiting() bool {
	return r.GetPhase() == domain.PhaseExiting
}

// ---------------- TUN 物理状态与精确生命周期 ----------------

func (r *RuntimeState) SetTunAlive(alive bool) { r.tunAlive.Store(alive) }
func (r *RuntimeState) IsTunAlive() bool       { return r.tunAlive.Load() }

func (r *RuntimeState) storeTime(target *atomic.Int64, t time.Time) {
	if t.IsZero() {
		target.Store(0)
	} else {
		target.Store(t.UnixNano())
	}
}

func (r *RuntimeState) loadTime(target *atomic.Int64) time.Time {
	nano := target.Load()
	if nano == 0 {
		return time.Time{}
	}
	return time.Unix(0, nano).Local()
}

// 启动收敛控制
func (r *RuntimeState) SetTunRequestedTime(t time.Time) { r.storeTime(&r.tunReqTime, t) }
func (r *RuntimeState) ClearTunStarting()               { r.storeTime(&r.tunReqTime, time.Time{}) }
func (r *RuntimeState) getTunRequestedTime() time.Time  { return r.loadTime(&r.tunReqTime) }

func (r *RuntimeState) IsTunStarting() bool {
	reqTime := r.getTunRequestedTime()
	return !reqTime.IsZero() && time.Since(reqTime) < TunInitGracePeriod
}

func (r *RuntimeState) IsTunStartingTimedOut() bool {
	reqTime := r.getTunRequestedTime()
	return !reqTime.IsZero() && time.Since(reqTime) >= TunInitGracePeriod
}

// 掉线防抖控制 (6s 掉线报警缓冲)
func (r *RuntimeState) SetTunLostTime(t time.Time) { r.storeTime(&r.tunLostTime, t) }
func (r *RuntimeState) ClearTunLost()              { r.storeTime(&r.tunLostTime, time.Time{}) }
func (r *RuntimeState) getTunLostTime() time.Time  { return r.loadTime(&r.tunLostTime) }

func (r *RuntimeState) IsTunDropDebouncing() bool {
	lostTime := r.getTunLostTime()
	return !lostTime.IsZero() && time.Since(lostTime) < TunLostAlarmDelay
}

// 设备名称绑定
func (r *RuntimeState) SetActualTunDevice(dev string) { r.tunDevName.Store(dev) }

func (r *RuntimeState) GetActualTunDevice() string {
	if v := r.tunDevName.Load(); v != nil {
		return v.(string)
	}
	return ""
}

func (r *RuntimeState) GetEffectiveTunDevice() string {
	if dev := r.GetActualTunDevice(); dev != "" {
		return dev
	}
	return domain.DefaultTunDevice
}

// ---------------- 系统主题状态 ----------------

func (r *RuntimeState) SetSystemTheme(theme domain.Theme) {
	r.systemTheme.Store(theme)
}

func (r *RuntimeState) GetSystemTheme() domain.Theme {
	if v := r.systemTheme.Load(); v != nil {
		if t, ok := v.(domain.Theme); ok {
			return t
		}
	}
	return domain.ThemeLight
}
