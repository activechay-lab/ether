package sessions

import "github.com/braedonsaunders/ether/internal/frontend"

// Binding and BindingTable live in internal/frontend
// (host_sessions_experience.go); generic types cannot be constructed
// through the factory interface.
type Binding[R any] = frontend.Binding[R]
type BindingTable[R any] = frontend.BindingTable[R]

func NewBindingTable[R any]() *BindingTable[R] { return frontend.NewBindingTable[R]() }
