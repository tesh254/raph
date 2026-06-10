package verbose

import (
	"fmt"
	"os"
	"sync/atomic"
)

var enabled atomic.Bool

func Set(value bool) {
	enabled.Store(value)
}

func Enabled() bool {
	if enabled.Load() {
		return true
	}
	return os.Getenv("RAPH_VERBOSE") == "1"
}

func Printf(format string, args ...any) {
	if !Enabled() {
		return
	}
	fmt.Fprintf(os.Stderr, "raph verbose: "+format+"\n", args...)
}
