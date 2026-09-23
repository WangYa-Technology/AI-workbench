//go:build !unix

package media

import "os"

func stageFreeBytes(*os.File) (uint64, error) { return 0, ErrStageStorage }
