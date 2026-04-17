package agent

import "sync"

// AgentState represents the lifecycle state of the client agent.
type AgentState string

const (
	StateInit           AgentState = "Init"
	StateAuthenticating AgentState = "Authenticating"
	StateRegistering    AgentState = "Registering"
	StateDiscovering    AgentState = "Discovering"
	StateConnecting     AgentState = "Connecting"
	StateRunning        AgentState = "Running"
	StateReconnecting   AgentState = "Reconnecting"
	StateStopped        AgentState = "Stopped"
)

// OnStateChange is invoked whenever agent state changes.
type OnStateChange func(from AgentState, to AgentState)

// StateMachine tracks current state and notifies listeners on transitions.
type StateMachine struct {
	mu        sync.RWMutex
	state     AgentState
	callbacks []OnStateChange
}

func NewStateMachine(initial AgentState) *StateMachine {
	return &StateMachine{state: initial}
}

func (s *StateMachine) Get() AgentState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

func (s *StateMachine) Set(next AgentState) {
	s.mu.Lock()
	previous := s.state
	if previous == next {
		s.mu.Unlock()
		return
	}
	s.state = next
	callbacks := append([]OnStateChange(nil), s.callbacks...)
	s.mu.Unlock()

	for _, cb := range callbacks {
		cb(previous, next)
	}
}

func (s *StateMachine) OnStateChange(callback OnStateChange) {
	if callback == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callbacks = append(s.callbacks, callback)
}
