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

func TestBatchTaskPathUsesQueryFilenameAndFallback(t *testing.T) {
	dir := t.TempDir()
	used := map[string]struct{}{}

	got := batchTaskPath("https://example.com/download?filename=%E5%8F%91%E5%B8%83.zip", dir, used)
	if want := filepath.Join(dir, "发布.zip"); got != want {
		t.Fatalf("batchTaskPath query filename = %q, want %q", got, want)
	}

	got = batchTaskPath("https://example.com/", dir, used)
	if want := filepath.Join(dir, "download.bin"); got != want {
		t.Fatalf("batchTaskPath fallback = %q, want %q", got, want)
	}
}
