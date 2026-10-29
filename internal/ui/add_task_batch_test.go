// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseDownloadURLsFiltersBlankAndDuplicates(t *testing.T) {
	got := parseDownloadURLs(" https://a.example/file.bin \n\nhttps://b.example/other.bin\nhttps://a.example/file.bin ")
	if len(got) != 2 || got[0] != "https://a.example/file.bin" || got[1] != "https://b.example/other.bin" {
		t.Fatalf("parseDownloadURLs = %v", got)
	}
}

func TestBatchTaskPathAvoidsCollisionsWithinSubmission(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "same.bin"), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	used := map[string]struct{}{}
	first := batchTaskPath("https://a.example/same.bin", dir, used)
	used[first] = struct{}{}
	second := batchTaskPath("https://b.example/same.bin", dir, used)
	if first == second {
		t.Fatalf("batch paths collided: %q", first)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("first batch path should be a new available name, got %q", first)
	}
}
