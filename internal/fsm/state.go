package fsm

import (
	"sync"
	"sync/atomic"
	"time"
)

type AppPhase int32

const (
	PhaseInitializing AppPhase = iota
	PhaseRunning
	PhaseExiting
	PhaseKernelPanic
)

type RuntimeState struct {
	phase        atomic.Int32
	tunAlive     atomic.Bool
	proxyActive  atomic.Bool

	isRestarting atomic.Bool
	isReloading  atomic.Bool

	timeMu       sync.RWMutex
	tunStartTime time.Time
	tunReqTime   time.Time
	tunLostTime  time.Time
	apiMuteUntil time.Time

	kernelErr    atomic.Value
}

func NewRuntimeState() *RuntimeState {
	rs := &RuntimeState{}
	rs.SetPhase(PhaseInitializing)
	rs.isRestarting.Store(false)
	rs.isReloading.Store(false)
	rs.kernelErr.Store("")
	return rs
}

func (r *RuntimeState) SetKernelError(errStr string) { r.kernelErr.Store(errStr) }
func (r *RuntimeState) GetKernelError() string       { return r.kernelErr.Load().(string) }

func (r *RuntimeState) SetRestarting(b bool) { r.isRestarting.Store(b) }
func (r *RuntimeState) IsRestarting() bool   { return r.isRestarting.Load() }
func (r *RuntimeState) SetReloading(b bool)  { r.isReloading.Store(b) }
func (r *RuntimeState) IsReloading() bool    { return r.isReloading.Load() }
func (r *RuntimeState) GetPhase() AppPhase   { return AppPhase(r.phase.Load()) }
func (r *RuntimeState) SetPhase(p AppPhase)  { r.phase.Store(int32(p)) }
func (r *RuntimeState) CompareAndSwapPhase(old, new AppPhase) bool {
	return r.phase.CompareAndSwap(int32(old), int32(new))
}

func (r *RuntimeState) ForceExitPhase() {
	for {
		curr := r.phase.Load()
		if AppPhase(curr) == PhaseExiting {
			return
		}
		if r.phase.CompareAndSwap(curr, int32(PhaseExiting)) {
			return
		}
	}
}

func (r *RuntimeState) IsExiting() bool            { return r.GetPhase() == PhaseExiting }
func (r *RuntimeState) SetTunAlive(alive bool)     { r.tunAlive.Store(alive) }
func (r *RuntimeState) IsTunAlive() bool           { return r.tunAlive.Load() }
func (r *RuntimeState) SetProxyActive(active bool) { r.proxyActive.Store(active) }
func (r *RuntimeState) IsProxyActive() bool        { return r.proxyActive.Load() }

func (r *RuntimeState) MuteAPIWatcher(d time.Duration) {
	r.timeMu.Lock()
	defer r.timeMu.Unlock()
	r.apiMuteUntil = time.Now().Add(d)
}

func (r *RuntimeState) IsAPIWatcherMuted() bool {
	r.timeMu.RLock()
	defer r.timeMu.RUnlock()
	if r.apiMuteUntil.IsZero() {
		return false
	}
	return time.Now().Before(r.apiMuteUntil)
}

func (r *RuntimeState) SetTunStartTime(t time.Time) {
	r.timeMu.Lock()
	defer r.timeMu.Unlock()
	r.tunStartTime = t
}

func (r *RuntimeState) GetTunStartTime() time.Time {
	r.timeMu.RLock()
	defer r.timeMu.RUnlock()
	return r.tunStartTime
}

func (r *RuntimeState) SetTunRequestedTime(t time.Time) {
	r.timeMu.Lock()
	defer r.timeMu.Unlock()
	r.tunReqTime = t
}

func (r *RuntimeState) GetTunRequestedTime() time.Time {
	r.timeMu.RLock()
	defer r.timeMu.RUnlock()
	return r.tunReqTime
}

func (r *RuntimeState) SetTunLostTime(t time.Time) {
	r.timeMu.Lock()
	defer r.timeMu.Unlock()
	r.tunLostTime = t
}

func (r *RuntimeState) GetTunLostTime() time.Time {
	r.timeMu.RLock()
	defer r.timeMu.RUnlock()
	return r.tunLostTime
}
