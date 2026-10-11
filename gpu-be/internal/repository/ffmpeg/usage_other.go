//go:build !linux

package ffmpeg

import "os"

func processMaxRSS(*os.ProcessState) int64 { return 0 }
