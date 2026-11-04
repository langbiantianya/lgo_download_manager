// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"reflect"
	"testing"

	"lgo_download_manager/internal/api"
)

func TestUIEventPumpCoalescesProgressAndKeepsStates(t *testing.T) {
	var scheduled []func()
	var got []api.Event
	p := newUIEventPump(func(fn func()) {
		scheduled = append(scheduled, fn)
	}, func(ev api.Event) {
		got = append(got, ev)
	})

	p.enqueue(api.Event{Why: "progress", Task: &api.Task{ID: "a", Downloaded: 1}})
	p.enqueue(api.Event{Why: "progress", Task: &api.Task{ID: "a", Downloaded: 2}})
	p.enqueue(api.Event{Why: "paused", Task: &api.Task{ID: "a", Status: api.StatusPaused}})
	p.enqueue(api.Event{Why: "progress", Task: &api.Task{ID: "a", Downloaded: 3}})
	p.enqueue(api.Event{Why: "added", Task: &api.Task{ID: "b"}})

	if len(scheduled) != 1 {
		t.Fatalf("scheduled %d callbacks, want 1", len(scheduled))
	}
	scheduled[0]()

	if len(got) != 3 {
		t.Fatalf("handled %d events, want 3: %+v", len(got), got)
	}
	if got[0].Why != "paused" || got[1].Why != "added" || got[2].Why != "progress" {
		t.Fatalf("event order = %v", []string{got[0].Why, got[1].Why, got[2].Why})
	}
	if got[2].Task.Downloaded != 3 {
		t.Fatalf("progress was not coalesced to latest snapshot: %+v", got[2].Task)
	}
}

func TestUIEventPumpDropsProgressBeforeState(t *testing.T) {
	var scheduled func()
	var got []api.Event
	p := newUIEventPump(func(fn func()) {
		scheduled = fn
	}, func(ev api.Event) {
		got = append(got, ev)
	})

	p.enqueue(api.Event{Why: "progress", Task: &api.Task{ID: "a", Downloaded: 9}})
	p.enqueue(api.Event{Why: "paused", Task: &api.Task{ID: "a", Status: api.StatusPaused}})
	scheduled()

	want := []string{"paused"}
	if len(got) != 1 || got[0].Why != want[0] {
		t.Fatalf("events = %+v, want only paused", got)
	}
}

func TestUIEventPumpDropsAllProgressBeforeDelete(t *testing.T) {
	var scheduled func()
	var got []api.Event
	p := newUIEventPump(func(fn func()) {
		scheduled = fn
	}, func(ev api.Event) {
		got = append(got, ev)
	})

	p.enqueue(api.Event{Why: "progress", Task: &api.Task{ID: "a", Downloaded: 9}})
	p.enqueue(api.Event{Why: "deleted"})
	scheduled()

	if len(got) != 1 || got[0].Why != "deleted" {
		t.Fatalf("events = %+v, want only deleted", got)
	}
}

func TestUIEventPumpSchedulesAgainAfterFlush(t *testing.T) {
	var scheduled int
	var got []api.Event
	p := newUIEventPump(func(func()) {
		scheduled++
	}, func(ev api.Event) {
		got = append(got, ev)
	})

	p.enqueue(api.Event{Why: "progress", Task: &api.Task{ID: "a"}})
	p.flush()
	p.enqueue(api.Event{Why: "progress", Task: &api.Task{ID: "a"}})

	if scheduled != 2 {
		t.Fatalf("scheduled %d callbacks, want 2", scheduled)
	}
	if len(got) != 1 {
		t.Fatalf("handled %d events before second flush, want 1", len(got))
	}
}

func TestUIEventPumpStopPreventsPendingFlush(t *testing.T) {
	var scheduled func()
	var got []api.Event
	p := newUIEventPump(func(fn func()) {
		scheduled = fn
	}, func(ev api.Event) {
		got = append(got, ev)
	})

	p.enqueue(api.Event{Why: "added", Task: &api.Task{ID: "a"}})
	p.stop()
	scheduled()

	if len(got) != 0 {
		t.Fatalf("stopped pump handled %+v", got)
	}
}

func TestUIEventPumpPreservesStateOrder(t *testing.T) {
	var scheduled func()
	var got []string
	p := newUIEventPump(func(fn func()) {
		scheduled = fn
	}, func(ev api.Event) {
		got = append(got, ev.Why)
	})

	for _, why := range []string{"started", "paused", "started", "completed"} {
		p.enqueue(api.Event{Why: why, Task: &api.Task{ID: "a"}})
	}
	scheduled()

	want := []string{"started", "paused", "started", "completed"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("state order = %v, want %v", got, want)
	}
}
