// Command edcolreport tails the Elite Dangerous journal and reports
// colonization progress to ravencolonial.com.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/pequalsnp/ed-colonization-reporter/internal/config"
	"github.com/pequalsnp/ed-colonization-reporter/internal/ui"
)

func main() {
	var (
		showConfigPath = flag.Bool("config-path", false, "print the config file path and exit")
		version        = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *version {
		fmt.Println("edcolreport (pre-release)")
		return
	}
	if *showConfigPath {
		p, err := config.Path()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(p)
		return
	}

	cfg, path, existed, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: config load failed (%v); using defaults\n", err)
	}
	if !existed {
		fmt.Fprintf(os.Stderr, "first run — config will be created at %s on save\n", path)
	}

	ui.New(cfg).Run()
}
