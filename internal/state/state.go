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

	currentAction atomic.Int32

	tunAlive    atomic.Bool
	tunReqTime  atomic.Int64
	tunLostTime atomic.Int64

	proxyRepairing atomic.Bool
	probeGen       atomic.Uint64
	tunDevName     atomic.Value
	profileLocks   sync.Map

	snapshotMu    sync.RWMutex
	activeAPIAddr string
	activeSecret  string
	activeUIName  string
}

func NewRuntimeState() *RuntimeState {
	rs := &RuntimeState{}
	rs.phase.Store(int32(domain.PhaseInitializing))
	return rs
}

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

func (r *RuntimeState) TryBeginReload() bool        { return r.TryBeginAction(ActionReload) }
func (r *RuntimeState) TryBeginRestart() bool       { return r.TryBeginAction(ActionRestart) }
func (r *RuntimeState) TryBeginSwitchProfile() bool { return r.TryBeginAction(ActionSwitchProfile) }

func (r *RuntimeState) IsProfileSwitching() bool { return r.CurrentAction() == ActionSwitchProfile }
func (r *RuntimeState) IsReloading() bool        { return r.CurrentAction() == ActionReload }
func (r *RuntimeState) IsRestarting() bool       { return r.CurrentAction() == ActionRestart }
func (r *RuntimeState) IsConfigSyncing() bool    { return r.CurrentAction() == ActionSyncAPI }

func (r *RuntimeState) SetProfileSwitching(b bool) {
	if b {
		r.TryBeginAction(ActionSwitchProfile)
	} else if r.CurrentAction() == ActionSwitchProfile {
		r.EndAction()
	}
}

func (r *RuntimeState) SetReloading(b bool) {
	if b {
		r.TryBeginAction(ActionReload)
	} else if r.CurrentAction() == ActionReload {
		r.EndAction()
	}
}

func (r *RuntimeState) SetRestarting(b bool) {
	if b {
		r.TryBeginAction(ActionRestart)
	} else if r.CurrentAction() == ActionRestart {
		r.EndAction()
	}
}

func (r *RuntimeState) SetConfigSyncing(b bool) {
	if b {
		r.TryBeginAction(ActionSyncAPI)
	} else if r.CurrentAction() == ActionSyncAPI {
		r.EndAction()
	}
}

func (r *RuntimeState) CanStartConfigTransaction() bool {
	return !r.IsExiting() && r.CurrentAction() == ActionNone
}

func (r *RuntimeState) UpdateWebUISnapshot(addr, secret, uiName string) {
	r.snapshotMu.Lock()
	defer r.snapshotMu.Unlock()
	r.activeAPIAddr = addr
	r.activeSecret = secret
	r.activeUIName = uiName
}

func (r *RuntimeState) GetWebUISnapshot() (string, string, string) {
	r.snapshotMu.RLock()
	defer r.snapshotMu.RUnlock()
	return r.activeAPIAddr, r.activeSecret, r.activeUIName
}

func (r *RuntimeState) TryAcquireProfileLock(path string) bool {
	_, loaded := r.profileLocks.LoadOrStore(path, true)
	return !loaded
}

func (r *RuntimeState) ReleaseProfileLock(path string) {
	r.profileLocks.Delete(path)
}

func (r *RuntimeState) GetPhase() domain.AppPhase { return domain.AppPhase(r.phase.Load()) }

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

func (r *RuntimeState) TryAcquireProxyRepair() bool { return r.proxyRepairing.CompareAndSwap(false, true) }
func (r *RuntimeState) ReleaseProxyRepair()         { r.proxyRepairing.Store(false) }

func (r *RuntimeState) AdvanceProbeGen() uint64 { return r.probeGen.Add(1) }
func (r *RuntimeState) GetProbeGen() uint64     { return r.probeGen.Load() }
