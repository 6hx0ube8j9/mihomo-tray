package state

import (
	"sync"
	"sync/atomic"
	"time"

	"mihomo-tray/internal/domain"
)

type KernelAction int32

const (
	ActionNone KernelAction = iota
	ActionSwitchProfile
	ActionReload
	ActionRestart
	ActionSyncAPI
)

type RuntimeState struct {
	phase atomic.Int32

	// 核心互斥动作状态机
	currentAction atomic.Int32

	tunAlive    atomic.Bool
	tunReqTime  atomic.Int64
	tunLostTime atomic.Int64

	proxyRepairing atomic.Bool
	probeGen       atomic.Uint64
	tunDevName     atomic.Value
	profileLocks   sync.Map
}

func NewRuntimeState() *RuntimeState {
	rs := &RuntimeState{}
	rs.phase.Store(int32(domain.PhaseInitializing))
	return rs
}

// ---------------- 核心排他动作状态机 ----------------

func (r *RuntimeState) TryBeginAction(action KernelAction) bool {
	if r.IsExiting() {
		return false
	}
	return r.currentAction.CompareAndSwap(int32(ActionNone), int32(action))
}

func (r *RuntimeState) EndAction() {
	r.currentAction.Store(int32(ActionNone))
}

func (r *RuntimeState) CurrentAction() KernelAction {
	return KernelAction(r.currentAction.Load())
}

func (r *RuntimeState) IsReloading() bool     { return r.CurrentAction() == ActionReload }
func (r *RuntimeState) IsRestarting() bool    { return r.CurrentAction() == ActionRestart }
func (r *RuntimeState) IsConfigSyncing() bool { return r.CurrentAction() == ActionSyncAPI }

func (r *RuntimeState) SetConfigSyncing(enable bool) {
	if enable {
		r.TryBeginAction(ActionSyncAPI)
	} else {
		r.currentAction.CompareAndSwap(int32(ActionSyncAPI), int32(ActionNone))
	}
}

// ---------------- 订阅单项并发锁 ----------------

func (r *RuntimeState) TryAcquireProfileLock(path string) bool {
	_, loaded := r.profileLocks.LoadOrStore(path, true)
	return !loaded
}

func (r *RuntimeState) ReleaseProfileLock(path string) {
	r.profileLocks.Delete(path)
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

// ---------------- TUN 状态与保护计时 ----------------

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

func (r *RuntimeState) SetTunRequestedTime(t time.Time) { r.storeTime(&r.tunReqTime, t) }
func (r *RuntimeState) GetTunRequestedTime() time.Time  { return r.loadTime(&r.tunReqTime) }
func (r *RuntimeState) SetTunLostTime(t time.Time)      { r.storeTime(&r.tunLostTime, t) }
func (r *RuntimeState) GetTunLostTime() time.Time       { return r.loadTime(&r.tunLostTime) }

func (r *RuntimeState) SetActualTunDevice(dev string) { r.tunDevName.Store(dev) }
func (r *RuntimeState) GetActualTunDevice() string {
	if v := r.tunDevName.Load(); v != nil {
		return v.(string)
	}
	return ""
}

// ---------------- 系统代理自动修复锁与探测代际 ----------------

func (r *RuntimeState) TryAcquireProxyRepair() bool {
	return r.proxyRepairing.CompareAndSwap(false, true)
}

func (r *RuntimeState) ReleaseProxyRepair() {
	r.proxyRepairing.Store(false)
}

func (r *RuntimeState) AdvanceProbeGen() uint64 { return r.probeGen.Add(1) }
func (r *RuntimeState) GetProbeGen() uint64     { return r.probeGen.Load() }
