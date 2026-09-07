package state

import (
	"sync/atomic"
	"time"
)

type AppPhase int32

const (
	PhaseInitializing AppPhase = iota
	PhaseRunning
	PhaseExiting
)

type RuntimeState struct {
	phase         atomic.Int32
	tunAlive      atomic.Bool
	isRestarting  atomic.Bool
	isReloading   atomic.Bool
	configSyncing atomic.Bool

	tunReqTime  atomic.Int64
	tunLostTime atomic.Int64
}

func NewRuntimeState() *RuntimeState {
	rs := &RuntimeState{}
	rs.phase.Store(int32(PhaseInitializing))
	return rs
}

func (r *RuntimeState) SetConfigSyncing(b bool) { r.configSyncing.Store(b) }
func (r *RuntimeState) IsConfigSyncing() bool   { return r.configSyncing.Load() }
func (r *RuntimeState) SetRestarting(b bool)    { r.isRestarting.Store(b) }
func (r *RuntimeState) IsRestarting() bool      { return r.isRestarting.Load() }
func (r *RuntimeState) SetReloading(b bool)     { r.isReloading.Store(b) }
func (r *RuntimeState) IsReloading() bool      { return r.isReloading.Load() }

func (r *RuntimeState) GetPhase() AppPhase { return AppPhase(r.phase.Load()) }

func (r *RuntimeState) SetPhase(p AppPhase) {
	for {
		curr := r.phase.Load()
		if AppPhase(curr) == PhaseExiting {
			return
		}
		if r.phase.CompareAndSwap(curr, int32(p)) {
			return
		}
	}
}

func (r *RuntimeState) ForceExitPhase() {
	r.phase.Store(int32(PhaseExiting))
}

func (r *RuntimeState) IsExiting() bool { 
	return r.GetPhase() == PhaseExiting 
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

func (r *RuntimeState) SetTunLostTime(t time.Time) { r.storeTime(&r.tunLostTime, t) }
func (r *RuntimeState) GetTunLostTime() time.Time  { return r.loadTime(&r.tunLostTime) }
