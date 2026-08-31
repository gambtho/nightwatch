// spike-headless proves the packaging chain without a GUI: state dir →
// embedded Postgres (initdb on first run) → tomte serve (migrations, then
// bind) → loopback ready → clean shutdown. Run it twice to prove warm
// restarts reuse the data directory.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/gambtho/tomte/app/internal/boot"
)

func main() {
	defState := filepath.Join(os.Getenv("HOME"), ".local", "share", "tomte-spike")
	stateDir := flag.String("state", defState, "state directory")
	serverBin := flag.String("server-bin", "tomte", "path to the tomte server binary")
	hold := flag.Duration("hold", 2*time.Second, "how long to hold the stack up before shutdown")
	flag.Parse()

	start := time.Now()
	sup, err := boot.Start(context.Background(), boot.Config{
		StateDir:  *stateDir,
		ServerBin: *serverBin,
		Logf:      log.Printf,
	})
	if err != nil {
		log.Fatalf("boot: %v", err)
	}
	log.Printf("READY in %s (first run: %v) — %s answering 401 on /v1/catalog", time.Since(start).Round(time.Millisecond), sup.FirstRun, sup.BaseURL)
	time.Sleep(*hold)
	sup.Stop()
	log.Printf("shut down cleanly")
}
