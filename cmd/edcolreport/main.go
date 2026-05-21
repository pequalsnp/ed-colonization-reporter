// Command edcolreport tails the Elite Dangerous journal and reports
// colonization progress to ravencolonial.com.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/pequalsnp/ed-colonization-reporter/internal/config"
	"github.com/pequalsnp/ed-colonization-reporter/internal/web"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	var (
		showConfigPath = flag.Bool("config-path", false, "print the config file path and exit")
		showVersion    = flag.Bool("version", false, "print version and exit")
		bind           = flag.String("bind", "127.0.0.1:0", "address to bind the local UI server (default: random loopback port)")
		noBrowser      = flag.Bool("no-browser", false, "don't open the browser automatically — just print the URL")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("edcolreport %s\n", version)
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := web.New(cfg)
	srv.Version = version
	srv.Bind = *bind
	srv.OpenBrowser = func(url string) {
		fmt.Printf("edcolreport %s listening on %s\n", version, url)
		if *noBrowser {
			return
		}
		if err := web.OpenBrowser(url); err != nil {
			fmt.Fprintf(os.Stderr, "could not open browser automatically (%v); open %s manually\n", err, url)
		}
	}

	if err := srv.Start(ctx); err != nil {
		log.Fatal(err)
	}
}
