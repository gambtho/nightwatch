// tomtectl gets a Tomte agent, defined by one agent-as-code YAML file,
// running on the Kubernetes cluster the user's kubeconfig points at.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/signal"

	"github.com/gambtho/tomte/tomtectl/internal/agentfile"
	"github.com/gambtho/tomte/tomtectl/internal/kube"
	"github.com/gambtho/tomte/tomtectl/internal/manifest"
)

const usage = `tomtectl — run an agent, defined as code, on Kubernetes

Usage:
  tomtectl init            scaffold an agent.yaml in the current directory
  tomtectl up              deploy the agent the file describes
  tomtectl status          show what is running
  tomtectl logs [--follow] show the agent's output
  tomtectl down            remove the agent from the cluster

init writes a fresh agent file to -f; every other command reads the
agent from -f (default ./agent.yaml) — the file is the single source
of truth. -n picks a namespace, --context a kubeconfig context.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "tomtectl:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return errors.New("a command is required")
	}
	cmd, rest := args[0], args[1:]

	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	file := fs.String("f", "agent.yaml", "path to the agent file")
	ns := fs.String("n", "", "namespace (default: the kubeconfig context's)")
	kctx := fs.String("context", "", "kubeconfig context (default: current)")
	follow := false
	if cmd == "logs" {
		fs.BoolVar(&follow, "follow", false, "keep streaming")
		fs.BoolVar(&follow, "F", false, "keep streaming (shorthand)")
	}
	if err := fs.Parse(rest); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	switch cmd {
	case "init":
		return initFile(*file)
	case "up", "status", "logs", "down":
		agent, raw, err := agentfile.Load(*file)
		if err != nil {
			return err
		}
		client, err := kube.New(*kctx, *ns)
		if err != nil {
			return err
		}
		name := agent.Metadata.Name
		switch cmd {
		case "up":
			cm, dep := manifest.Objects(agent, raw)
			if err := client.Apply(ctx, cm, dep); err != nil {
				return err
			}
			fmt.Printf("agent %q is on its way up in namespace %q — try `tomtectl status`, then `tomtectl logs --follow`\n",
				name, client.Namespace)
			return nil
		case "status":
			return client.Status(ctx, name, os.Stdout)
		case "logs":
			err := client.Logs(ctx, name, follow, os.Stdout)
			// Ctrl-C while following is how streaming ends, not a failure.
			if errors.Is(err, context.Canceled) && ctx.Err() != nil {
				return nil
			}
			return err
		case "down":
			removed, err := client.Delete(ctx, name)
			if err != nil {
				return err
			}
			if !removed {
				fmt.Printf("agent %q was not deployed in namespace %q — nothing to remove\n", name, client.Namespace)
				return nil
			}
			fmt.Printf("agent %q removed from namespace %q\n", name, client.Namespace)
			return nil
		}
		return nil
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		fmt.Print(usage)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func initFile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists — edit it, or pass -f for a new path", path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("checking %s: %w", path, err)
	}
	if err := os.WriteFile(path, agentfile.Template, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s — read it (it is the agent), then run `tomtectl up`\n", path)
	return nil
}
