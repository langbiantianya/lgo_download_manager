//go:build !linux && !darwin && !windows

package prealloc

import (
	"fmt"
	"os"
)

func platformPrealloc(f *os.File, size int64) error {
	// Generic unix without a curated preallocation call: force callers
	// into the truncate+probe-write fallback.
	return fmt.Errorf("no native prealloc on this unix; using fallback")
}
