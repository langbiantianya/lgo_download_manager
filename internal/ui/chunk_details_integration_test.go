package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

// TestMosaicWithRealEngineOutput simulates the data shape the scheduler
// sends after the engine has planned its chunks. chunkProgress is
// offset-from-start; chunkRanges is [start0,end0,start1,end1,...] with
// inclusive end (matching engine.Job.Chunks() and the persist code in
// scheduler.Start).
func TestMosaicWithRealEngineOutput(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	const total int64 = 4 * 1024 * 1024
	ranges := []int64{0, 1048575, 1048576, 2097151, 2097152, 3145727, 3145728, 4194303}
	progress := []int64{1048576, 1048576, 0, 0}

	m := newChunkMosaic("task-x", nil)
	m.resizeForTotal(total)
	m.update(progress, ranges, total)

	if m.tiles[0].FillColor != theme.Color(theme.ColorNameSuccess) {
		t.Errorf("tile 0 fill = %v, want Success", m.tiles[0].FillColor)
	}
	if m.tiles[1].FillColor != theme.Color(theme.ColorNameSuccess) {
		t.Errorf("tile 1 fill = %v, want Success", m.tiles[1].FillColor)
	}
	if m.tiles[2].FillColor == theme.Color(theme.ColorNameSuccess) {
		t.Errorf("tile 2 should not be Success yet")
	}
	if m.tiles[3].FillColor == theme.Color(theme.ColorNameSuccess) {
		t.Errorf("tile 3 should not be Success yet")
	}
}

// TestMosaicEmptyRangesFallsBack verifies that when chunkRanges is empty
// (e.g. task not yet started, or a pre-migration row), the window still
// renders something based on chunkProgress alone.
func TestMosaicEmptyRangesFallsBack(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	const total int64 = 4 * 1024 * 1024
	progress := []int64{1048576, 1048576, 0, 0} // 4 threads, 2 done

	m := newChunkMosaic("task-x", nil)
	m.resizeForTotal(total)
	m.update(progress, nil, total)

	// Fallback assumes evenly-split ranges; first half should be green.
	if m.tiles[0].FillColor != theme.Color(theme.ColorNameSuccess) {
		t.Errorf("tile 0 fill = %v, want Success (fallback)", m.tiles[0].FillColor)
	}
	if m.tiles[1].FillColor != theme.Color(theme.ColorNameSuccess) {
		t.Errorf("tile 1 fill = %v, want Success (fallback)", m.tiles[1].FillColor)
	}
}