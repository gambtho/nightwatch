// tomtectl gets a Tomte agent, defined by one agent-as-code YAML file,
// running on the Kubernetes cluster the user's kubeconfig points at.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"

	"golang.org/x/term"

	"github.com/gambtho/tomte/tomtectl/internal/agentfile"
	"github.com/gambtho/tomte/tomtectl/internal/kube"
	"github.com/gambtho/tomte/tomtectl/internal/manifest"
)

const usage = `tomtectl — run an agent, defined as code, on Kubernetes

Usage:
  tomtectl init            scaffold an agent.yaml in the current directory
  tomtectl set-key         store the LLM API key in the Secret the file names
  tomtectl up [--image]    deploy the agent the file describes
  tomtectl status          show what is running
  tomtectl logs [--follow] show the agent's output
  tomtectl down            remove the agent from the cluster

init writes a fresh agent file to -f; every other command reads the
agent from -f (default ./agent.yaml) — the file is the single source
of truth. -n picks a namespace, --context a kubeconfig context.

set-key reads the key from stdin (hidden prompt on a terminal, or
piped) and writes it to the Kubernetes Secret spec.llm.secretRef
names — the key never appears in the YAML, a ConfigMap, or logs.
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
	image := manifest.DefaultImage
	if cmd == "up" {
		fs.StringVar(&image, "image", manifest.DefaultImage, "agent runtime image")
	}
	if err := fs.Parse(rest); err != nil {
		return err
	}
	// No command takes positional arguments — the agent always comes
	// from -f. Silently ignoring one (`tomtectl down other-agent`)
	// would act on a different agent than the user named.
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q — the agent is picked by -f, not by name (see `tomtectl help`)", fs.Arg(0))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	switch cmd {
	case "init":
		return initFile(*file)
	case "up", "status", "logs", "down", "set-key":
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
		case "set-key":
			return setKey(ctx, client, agent)
		case "up":
			if ref := agent.Spec.LLM.SecretRef; ref != "" {
				ok, err := client.SecretExists(ctx, ref)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("secret %q does not exist in namespace %q — run `tomtectl set-key` first", ref, client.Namespace)
				}
			}
			cm, dep := manifest.Objects(agent, raw, image)
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

// setKey writes the API key into the Secret spec.llm.secretRef names.
// The key arrives on stdin — never argv (visible in process lists and
// shell history) and never an env var — and is never echoed.
func setKey(ctx context.Context, client *kube.Client, agent *agentfile.Agent) error {
	ref := agent.Spec.LLM.SecretRef
	if ref == "" {
		return errors.New("spec.llm.secretRef is not set — the agent has no key to store (a local llm is keyless)")
	}
	key, err := readKey()
	if err != nil {
		return err
	}
	if err := client.ApplySecret(ctx, manifest.SecretForKey(ref, agent.Metadata.Name, key)); err != nil {
		return err
	}
	fmt.Printf("secret %q in namespace %q now holds the key for agent %q — run `tomtectl up`\n",
		ref, client.Namespace, agent.Metadata.Name)
	return nil
}

func readKey() ([]byte, error) {
	var raw []byte
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(os.Stderr, "Paste the API key (input hidden): ")
		key, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return nil, err
		}
		raw = key
	} else {
		key, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, err
		}
		raw = key
	}
	key := bytes.TrimSpace(raw)
	if len(key) == 0 {
		return nil, errors.New("no key on stdin")
	}
	return key, nil
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
