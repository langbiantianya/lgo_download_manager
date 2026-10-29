// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package main

import (
	"path/filepath"
	"testing"

	"lgo_download_manager/internal/downloaddir"
	"lgo_download_manager/internal/store"
)

func TestStartupModeAutostartOnlyHidesInitialWindow(t *testing.T) {
	mode := startupMode{autostart: true}
	if mode.startUIOnLaunch() {
		t.Fatal("autostart should keep the UI hidden at launch")
	}
	if mode.addURLDirectly() {
		t.Fatal("autostart must not turn later URL hand-offs into direct downloads")
	}
}

func TestStartupModeNoGUIDirectlyAddsURL(t *testing.T) {
	mode := startupMode{noGUI: true}
	if mode.startUIOnLaunch() {
		t.Fatal("no-gui should not start the UI")
	}
	if !mode.addURLDirectly() {
		t.Fatal("no-gui should add URL hand-offs without a dialog")
	}
}

func TestSavePathForUsesSystemDownloadDirWhenUnset(t *testing.T) {
	got := savePathFor(store.Settings{}, "https://example.com/archive.zip", "")
	want := filepath.Join(downloaddir.Default(), "archive.zip")
	if got != want {
		t.Fatalf("savePathFor = %q, want %q", got, want)
	}
}
