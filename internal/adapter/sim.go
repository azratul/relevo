package adapter

import (
	"context"
	"sync"

	"relevo/internal/protocol"
)

// Sim is a simulated backend: it records chords instead of injecting them.
// It also lets tests and demos toggle focus and failures.
type Sim struct {
	mu       sync.Mutex
	Log      []string
	Focused  bool // whether the simulated target "has focus"
	FailNext error
	OnSend   func(Window, Chord)
}

func NewSim() *Sim { return &Sim{Focused: true} }

func (s *Sim) Name() string          { return "sim" }
func (s *Sim) Platform() string      { return "any/simulated" }
func (s *Sim) FocusCheck() string    { return protocol.FocusVerified }
func (s *Sim) Confirmation() string  { return protocol.ConfirmSimulated }
func (s *Sim) Permissions() []string { return nil }
func (s *Sim) Check(context.Context) error {
	return nil
}

var simWindow = Window{ID: "sim-1", Class: "relevo-sim", Title: "Simulated presentation"}

func (s *Sim) Windows(context.Context) ([]Window, error) {
	return []Window{simWindow, {ID: "sim-2", Class: "chat", Title: "Video call"}}, nil
}

func (s *Sim) Active(context.Context) (Window, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Focused {
		return simWindow, nil
	}
	return Window{ID: "sim-2", Class: "chat", Title: "Video call"}, nil
}

func (s *Sim) SetFocused(v bool) { s.mu.Lock(); s.Focused = v; s.mu.Unlock() }

func (s *Sim) Send(_ context.Context, w Window, c Chord) error {
	s.mu.Lock()
	if err := s.FailNext; err != nil {
		s.FailNext = nil
		s.mu.Unlock()
		return err
	}
	s.Log = append(s.Log, c.String())
	cb := s.OnSend
	s.mu.Unlock()
	if cb != nil {
		cb(w, c)
	}
	return nil
}

func (s *Sim) Sent() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.Log...)
}

// SetOnSend installs a callback run after each simulated chord.
func (s *Sim) SetOnSend(f func(Window, Chord)) { s.mu.Lock(); s.OnSend = f; s.mu.Unlock() }
