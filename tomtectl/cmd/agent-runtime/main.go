// agent-runtime is the program inside the agent pod: it reads the
// mounted agent.yaml and runs it (see internal/runtime). With the one
// argument "stub" it instead serves the minimal OpenAI-compatible
// endpoint the kind e2e talks to.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/gambtho/tomte/tomtectl/internal/manifest"
	"github.com/gambtho/tomte/tomtectl/internal/runtime"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "stub" {
		key := os.Getenv("TOMTE_STUB_KEY")
		if key == "" {
			log.Fatal("tomte llm stub: TOMTE_STUB_KEY is empty — the stub only serves authenticated requests")
		}
		log.Print("tomte llm stub listening on :8080")
		log.Fatal(http.ListenAndServe(":8080", runtime.StubHandler(key)))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	client := &http.Client{Timeout: 5 * time.Minute}
	err := runtime.Loop(ctx, "/tomte/agent.yaml", os.Getenv(manifest.APIKeyEnv), os.Stdout, client)
	if err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "tomte agent:", err)
		os.Exit(1)
	}
}
