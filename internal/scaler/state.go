package scaler

import "time"

type ServiceState struct {
	EMA           float64
	LastScaleTime time.Time
	SimulatedReplicas *uint64
}

type StateStore struct {
	Services map[string]*ServiceState
}

func NewStateStore() *StateStore {
	return &StateStore{
		Services: make(map[string]*ServiceState),
	}
}
