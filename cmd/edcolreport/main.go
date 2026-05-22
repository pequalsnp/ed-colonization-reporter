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
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/config"
	"github.com/pequalsnp/ed-colonization-reporter/internal/gui"
	"github.com/pequalsnp/ed-colonization-reporter/internal/web"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	var (
		showConfigPath = flag.Bool("config-path", false, "print the config file path and exit")
		showVersion    = flag.Bool("version", false, "print version and exit")
		bind           = flag.String("bind", "127.0.0.1:0", "address to bind the local OAuth callback server (default: random loopback port)")
		headless       = flag.Bool("headless", false, "run without opening the Fyne window (HTTP-only mode for debugging)")
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
	// OpenBrowser is now a no-op for the desktop UI — the Fyne window is
	// the primary surface, and the browser only gets opened when the
	// Frontier sign-in button is clicked. We still call it once when the
	// listener binds, just to print the URL for diagnostics.
	srv.OpenBrowser = func(url string) {
		fmt.Printf("edcolreport %s — backend listening on %s\n", version, url)
	}

	// Backend runs in a goroutine. Fyne's event loop owns main.
	srvErr := make(chan error, 1)
	go func() { srvErr <- srv.Start(ctx) }()

	// Wait for the backend listener to bind so the OAuth flow has a port.
	// The server prints the URL once bound; URL() goes non-empty too.
	for srv.URL() == "" {
		select {
		case e := <-srvErr:
			if e != nil {
				log.Fatal(e)
			}
			return
		case <-ctx.Done():
			return
		case <-time.After(20 * time.Millisecond):
		}
	}

	if *headless {
		fmt.Fprintln(os.Stderr, "running headless; press Ctrl+C to exit")
		<-ctx.Done()
		<-srvErr
		return
	}

	gui.Run(ctx, srv)
	// Fyne returned (user closed the window). Tear down the backend.
	stop()
	<-srvErr
}
