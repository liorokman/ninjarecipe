// Package events is the in-process, post-commit event bus (20 §15).
package events

import "time"

// Event is published after a transaction commits.
type Event interface {
	Type() string
	OccurredAt() time.Time
}

// Base carries the common fields.
type Base struct {
	RecipeID string    `json:"recipe_id"`
	ActorID  string    `json:"actor_id"`
	At       time.Time `json:"at"`
}

func (b Base) OccurredAt() time.Time { return b.At }

type RecipeCreated struct{ Base }
type RecipeUpdated struct{ Base }
type RecipeDeleted struct{ Base }
type InboxItemReceived struct{ Base }
type InboxItemAccepted struct{ Base }

func (RecipeCreated) Type() string     { return "RecipeCreated" }
func (RecipeUpdated) Type() string     { return "RecipeUpdated" }
func (RecipeDeleted) Type() string     { return "RecipeDeleted" }
func (InboxItemReceived) Type() string { return "InboxItemReceived" }
func (InboxItemAccepted) Type() string { return "InboxItemAccepted" }
