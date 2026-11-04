// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"sync"

	"lgo_download_manager/internal/api"
)

// uiEventPump coalesces high-frequency progress events before handing work to
// the Fyne event loop. State events are kept in order. A progress event older
// than the latest state event for the same task is discarded, so a queued
// "downloading" snapshot cannot overwrite a later "paused" state.
type uiEventPump struct {
	mu        sync.Mutex
	states    []api.Event
	progress  map[string]api.Event
	scheduled bool
	stopped   bool

	schedule func(func())
	handle   func(api.Event)
}

func newUIEventPump(schedule func(func()), handle func(api.Event)) *uiEventPump {
	return &uiEventPump{
		progress: make(map[string]api.Event),
		schedule: schedule,
		handle:   handle,
	}
}

// enqueue accepts one event from the RPC subscription goroutine.
func (p *uiEventPump) enqueue(ev api.Event) {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}

	if ev.Why == "progress" && ev.Task != nil {
		p.progress[ev.Task.ID] = ev
	} else {
		if ev.Task == nil && ev.Why == "deleted" {
			clear(p.progress)
		} else if ev.Task != nil {
			delete(p.progress, ev.Task.ID)
		}
		p.states = append(p.states, ev)
	}

	if p.scheduled {
		p.mu.Unlock()
		return
	}
	p.scheduled = true
	p.mu.Unlock()
	p.schedule(p.flush)
}

// flush runs on the Fyne event thread and applies one coalesced batch.
func (p *uiEventPump) flush() {
	p.mu.Lock()
	if p.stopped {
		p.scheduled = false
		p.mu.Unlock()
		return
	}

	states := p.states
	p.states = nil
	progress := make([]api.Event, 0, len(p.progress))
	for _, ev := range p.progress {
		progress = append(progress, ev)
	}
	clear(p.progress)
	p.scheduled = false
	p.mu.Unlock()

	// State transitions establish the authoritative task status. Apply them
	// first, then paint the latest progress snapshot collected afterwards.
	for _, ev := range states {
		p.handle(ev)
	}
	for _, ev := range progress {
		p.handle(ev)
	}
}

func (p *uiEventPump) stop() {
	p.mu.Lock()
	p.stopped = true
	p.states = nil
	clear(p.progress)
	p.mu.Unlock()
}
