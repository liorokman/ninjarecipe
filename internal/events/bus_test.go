package events

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestBusDeliversAfterPublishAndDrainsOnClose(t *testing.T) {
	b := NewBus(10)
	var mu sync.Mutex
	var got []string
	b.Subscribe("RecipeCreated", func(ctx context.Context, e Event) {
		mu.Lock()
		got = append(got, e.(RecipeCreated).RecipeID)
		mu.Unlock()
	})
	b.Subscribe("RecipeCreated", func(ctx context.Context, e Event) { panic("handler panics are contained") })
	b.Publish(RecipeCreated{Base{RecipeID: "a", At: time.Now()}})
	b.Publish(RecipeDeleted{Base{RecipeID: "ignored"}})
	b.Publish(nil)
	b.Publish(RecipeCreated{Base{RecipeID: "b"}})
	b.Close()
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("%v", got)
	}
}

func TestBusDropsWhenFull(t *testing.T) {
	b := NewBus(1)
	block := make(chan struct{})
	b.Subscribe("InboxItemReceived", func(ctx context.Context, e Event) { <-block })
	for i := 0; i < 50; i++ {
		b.Publish(InboxItemReceived{}) // must never block the caller
	}
	close(block)
	b.Close()
}

func TestTypes(t *testing.T) {
	for e, want := range map[Event]string{RecipeCreated{}: "RecipeCreated", RecipeUpdated{}: "RecipeUpdated", RecipeDeleted{}: "RecipeDeleted",
		InboxItemReceived{}: "InboxItemReceived", InboxItemAccepted{}: "InboxItemAccepted"} {
		if e.Type() != want {
			t.Fatal(want)
		}
	}
}
