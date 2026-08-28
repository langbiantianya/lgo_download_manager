package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

// TestMosaicTilesTurnGreen verifies that once chunkProgress says chunk i
// is fully written, the corresponding 1 MiB tiles turn to the success
// color (green).
func TestMosaicTilesTurnGreen(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	// 4 MiB file, 4 chunks of 1 MiB each.
	const total int64 = 4 * 1024 * 1024
	ranges := []int64{
		0, 1024*1024 - 1,
		1024 * 1024, 2*1024*1024 - 1,
		2 * 1024 * 1024, 3*1024*1024 - 1,
		3 * 1024 * 1024, 4*1024*1024 - 1,
	}
	// Chunk 0 done, 1 done, 2 half done, 3 not started.
	progress := []int64{1024 * 1024, 1024 * 1024, 512 * 1024, 0}

	m := newChunkMosaic("task-x", nil)
	m.resizeForTotal(total)
	m.update(progress, ranges, total)

	if got := m.tiles[0].FillColor; got != theme.Color(theme.ColorNameSuccess) {
		t.Errorf("tile 0 fill = %v, want Success", got)
	}
	if got := m.tiles[1].FillColor; got != theme.Color(theme.ColorNameSuccess) {
		t.Errorf("tile 1 fill = %v, want Success", got)
	}
	if got := m.tiles[2].FillColor; got == theme.Color(theme.ColorNameSuccess) {
		t.Errorf("tile 2 fill = Success, want Background (chunk half-done)")
	}
	if got := m.tiles[3].FillColor; got == theme.Color(theme.ColorNameSuccess) {
		t.Errorf("tile 3 fill = Success, want Background (chunk empty)")
	}
}

// TestMosaicAllComplete: with all chunks done, every tile should be green.
func TestMosaicAllComplete(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	const total int64 = 4 * 1024 * 1024
	ranges := []int64{
		0, 1024*1024 - 1,
		1024 * 1024, 2*1024*1024 - 1,
		2 * 1024 * 1024, 3*1024*1024 - 1,
		3 * 1024 * 1024, 4*1024*1024 - 1,
	}
	progress := []int64{1024 * 1024, 1024 * 1024, 1024 * 1024, 1024 * 1024}

	m := newChunkMosaic("task-x", nil)
	m.resizeForTotal(total)
	m.update(progress, ranges, total)

	for i, tile := range m.tiles {
		if tile.FillColor != theme.Color(theme.ColorNameSuccess) {
			t.Errorf("tile %d fill = %v, want Success", i, tile.FillColor)
		}
	}
}