// Package core provides a declarative finite state machine runtime
// for defining and executing multi-step trading strategies on perp DEXes.
//
// A Strategy is a directed graph of States connected by Transitions.
// The Engine evaluates transitions on every tick and drives the FSM.
package core

// Strategy is a complete state machine definition.
// Implement this interface to define any trading strategy.
type Strategy interface {
	// Name returns a human-readable identifier used in logs and metrics.
	Name() string

	// Initial returns the name of the starting state.
	Initial() string

	// States returns all valid states in the strategy graph.
	// Every state referenced by a Transition must appear here.
	States() []State

	// Transitions returns all valid edges in the strategy graph.
	// Evaluated in order; first matching Guard wins.
	Transitions() []Transition
}

// State is a named node in the strategy graph.
type State struct {
	// Name uniquely identifies the state within its Strategy.
	Name string

	// OnEnter is called once when the engine enters this state.
	// Use it to submit orders, reset counters, log entry.
	OnEnter func(*Context) error

	// OnTick is called every engine interval while in this state.
	// Use it for periodic actions: slice submissions, status checks.
	OnTick func(*Context) error

	// OnExit is called once just before leaving this state.
	// Use it to cancel pending orders, flush state.
	OnExit func(*Context) error
}

// Transition is a conditional edge from one State to another.
type Transition struct {
	// From is the source state name. "*" matches any state.
	From string

	// To is the destination state name.
	To string

	// Guard is the condition evaluated each tick.
	// Transition fires when Guard returns true.
	Guard func(*Context) bool

	// Action is an optional side-effect executed on transition,
	// after OnExit of From and before OnEnter of To.
	Action func(*Context) error
}
