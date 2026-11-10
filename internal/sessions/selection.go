package sessions

import "github.com/braedonsaunders/ether/internal/frontend"

// Selections lives in internal/frontend (host_sessions_experience.go);
// generic types cannot be constructed through the factory interface.
type Selections[T any] = frontend.Selections[T]

func NewSelections[T any]() *Selections[T] { return frontend.NewSelections[T]() }
