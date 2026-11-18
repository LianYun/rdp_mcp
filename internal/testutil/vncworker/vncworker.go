// Package vncworker lets a test binary act as `remoter-mcp vnc-worker --ipc SOCK`
// (set Env in the test and call MaybeRun from TestMain).
package vncworker

import (
	"fmt"
	"os"

	"github.com/LianYun/rdp_mcp/internal/vnc"
)

// Env is the variable that switches the test binary into worker mode.
const Env = "REMOTER_TEST_VNC_WORKER"

// MaybeRun runs the VNC worker and exits if the binary was started as one.
func MaybeRun() {
	if os.Getenv(Env) != "1" {
		return
	}
	for i, a := range os.Args {
		if a == "--ipc" && i+1 < len(os.Args) {
			if err := vnc.RunWorker(os.Args[i+1], "test"); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
}
