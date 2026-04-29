// Command gwim is the entrypoint binary for the Golang Window Manager.
//
// On macOS it runs as an LSUIElement (background agent) and exposes its
// controls through a menu-bar icon. The platform-specific bootstrap lives
// in main_<os>.go, leaving this file responsible only for flag parsing
// and version reporting.
//
// The main package sits at the module root so that
//
//	go install github.com/nealhardesty/gwim@latest
//
// produces a working `gwim` binary in $GOBIN. Building the macOS .app
// bundle (with stable codesign identity for TCC) still requires
// `make app` from a checkout — see README.md.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.BoolVar(showVersion, "v", false, "Alias for -version")
	flag.Parse()

	if *showVersion {
		fmt.Printf("gwim %s\n", Version)
		return
	}

	if err := startApp(); err != nil {
		fmt.Fprintln(os.Stderr, "gwim:", err)
		os.Exit(1)
	}
}
