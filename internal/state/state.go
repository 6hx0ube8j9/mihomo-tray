package state

import (
	"context"
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

type PreemptStatus int

const (
	PreemptAcquired PreemptStatus = iota
	PreemptSuperseded
	PreemptBusy
)

type ActionClaim struct {
	Ctx      context.Context
	Finish   func()
	PrevDone <-chan struct{}
	WaitSeq  uint64
	Acquired bool
	Waiting  bool
}

type activeAction struct {
	preemptible bool
	cancel      context.CancelFunc
	done        chan struct{}
}

type RuntimeState struct {
	phase atomic.Int32

	actionMu      sync.Mutex
	activeAction  *activeAction
	waiterSeq     uint64
	currentAction atomic.Int32

	tunAlive    atomic.Bool
	tunReqTime  atomic.Int64
	tunLostTime atomic.Int64

	proxyRepairing atomic.Bool
	tunDevName     atomic.Value
	systemTheme    atomic.Value
	profileLocks   sync.Map
}

func NewRuntimeState() *RuntimeState {
	rs := &RuntimeState{}
	rs.phase.Store(int32(domain.PhaseInitializing))
	rs.systemTheme.Store(domain.ThemeLight)
	return rs
}

func (r *RuntimeState) IsActionBusy() bool {
	return r.CurrentAction() != ActionNone
}

// ---------------- 核心排他动作状态机 ----------------

func (r *RuntimeState) AcquireAction(parent context.Context, action KernelAction, preemptible, allowPreempt bool) ActionClaim {
	r.actionMu.Lock()
	if r.IsExiting() {
		r.actionMu.Unlock()
		return ActionClaim{}
	}

	if r.activeAction == nil {
		if allowPreempt {
			r.waiterSeq++
		}
		ctx, finish := r.startActionLocked(parent, action, preemptible)
		r.actionMu.Unlock()
		return ActionClaim{
			Ctx:      ctx,
			Finish:   finish,
			Acquired: true,
		}
	}

	if allowPreempt && r.activeAction.preemptible {
		r.waiterSeq++
		seq := r.waiterSeq
		cancel := r.activeAction.cancel
		done := r.activeAction.done
		r.actionMu.Unlock()

		cancel()
		return ActionClaim{
			PrevDone: done,
			WaitSeq:  seq,
			Waiting:  true,
		}
	}

	r.actionMu.Unlock()
	return ActionClaim{}
}

func (r *RuntimeState) CompletePreempt(parent context.Context, action KernelAction, preemptible bool, waitSeq uint64) (context.Context, func(), PreemptStatus) {
	r.actionMu.Lock()
	defer r.actionMu.Unlock()

	if r.IsExiting() || r.waiterSeq != waitSeq {
		return nil, nil, PreemptSuperseded
	}
	if r.activeAction != nil {
		return nil, nil, PreemptBusy
	}

	ctx, finish := r.startActionLocked(parent, action, preemptible)
	return ctx, finish, PreemptAcquired
}

func (r *RuntimeState) startActionLocked(parent context.Context, action KernelAction, preemptible bool) (context.Context, func()) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	act := &activeAction{
		preemptible: preemptible,
		cancel:      cancel,
		done:        done,
	}
	r.activeAction = act
	r.currentAction.Store(int32(action))

	var once sync.Once
	finish := func() {
		once.Do(func() {
			r.actionMu.Lock()
			if r.activeAction == act {
				r.activeAction = nil
				r.currentAction.Store(int32(ActionNone))
			}
			r.actionMu.Unlock()

			cancel()
			close(done)
		})
	}
	return ctx, finish
}

func (r *RuntimeState) CurrentAction() KernelAction {
	return KernelAction(r.currentAction.Load())
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
func (r *RuntimeState) GetTunRequestedTime() time.Time  { return r.loadTime(&r.tunReqTime) }
func (r *RuntimeState) ClearTunStarting()               { r.storeTime(&r.tunReqTime, time.Time{}) }
func (r *RuntimeState) IsTunStarting() bool {
	reqTime := r.GetTunRequestedTime()
	return !reqTime.IsZero() && time.Since(reqTime) < TunInitGracePeriod
}
func (r *RuntimeState) IsTunStartingTimedOut() bool {
	reqTime := r.GetTunRequestedTime()
	return !reqTime.IsZero() && time.Since(reqTime) >= TunInitGracePeriod
}

// 掉线防抖控制 (6s 掉线报警缓冲)
func (r *RuntimeState) SetTunLostTime(t time.Time) { r.storeTime(&r.tunLostTime, t) }
func (r *RuntimeState) GetTunLostTime() time.Time  { return r.loadTime(&r.tunLostTime) }
func (r *RuntimeState) ClearTunLost()              { r.storeTime(&r.tunLostTime, time.Time{}) }
func (r *RuntimeState) IsTunDropDebouncing() bool {
	lostTime := r.GetTunLostTime()
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

// ---------------- 系统代理自动修复锁 ----------------

func (r *RuntimeState) TryAcquireProxyRepair() bool {
	return r.proxyRepairing.CompareAndSwap(false, true)
}

func (r *RuntimeState) ReleaseProxyRepair() {
	r.proxyRepairing.Store(false)
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
