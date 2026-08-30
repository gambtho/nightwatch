package compute

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// RunnerFunc executes one run. stateDir is the actor's persistent working
// directory: it survives across invocations of the same actor.
type RunnerFunc func(ctx context.Context, req InvokeRequest, stateDir string)

type Local struct {
	baseDir string
	runner  RunnerFunc
	wg      sync.WaitGroup

	mu     sync.Mutex
	actors map[ActorID]*sync.Mutex
}

func NewLocal(baseDir string, runner RunnerFunc) *Local {
	return &Local{
		baseDir: baseDir,
		runner:  runner,
		actors:  make(map[ActorID]*sync.Mutex),
	}
}

func (l *Local) dir(a ActorID) string {
	return filepath.Join(l.baseDir, string(a))
}

func (l *Local) lockFor(a ActorID) *sync.Mutex {
	l.mu.Lock()
	defer l.mu.Unlock()
	m, ok := l.actors[a]
	if !ok {
		m = &sync.Mutex{}
		l.actors[a] = m
	}
	return m
}

func (l *Local) EnsureActor(ctx context.Context, w WorkflowRef, tmpl TemplateRef) (ActorID, error) {
	a := ActorID(filepath.Join(w.TenantID.String(), w.WorkflowID.String()))
	if err := os.MkdirAll(l.dir(a), 0o755); err != nil {
		return "", err
	}
	return a, nil
}

func (l *Local) Invoke(ctx context.Context, a ActorID, payload InvokeRequest) (Handle, error) {
	dir := l.dir(a)
	if _, err := os.Stat(dir); err != nil {
		return Handle{}, fmt.Errorf("compute: unknown actor %q: %w", a, err)
	}
	l.wg.Add(1)
	// The run outlives the HTTP request that fired it.
	runCtx := context.WithoutCancel(ctx)
	go func() {
		defer l.wg.Done()
		// One actor, one run at a time: the spec's overlap policy is
		// "default to serialize", and the shared state directory makes
		// concurrent runs a data race.
		m := l.lockFor(a)
		m.Lock()
		defer m.Unlock()
		l.runner(runCtx, payload, dir)
	}()
	return Handle{ActorID: a, RunID: payload.RunID}, nil
}

// Suspend is a no-op locally; suspension is Substrate's economic premise,
// not ours to fake here.
func (l *Local) Suspend(ctx context.Context, a ActorID) error { return nil }

func (l *Local) Destroy(ctx context.Context, a ActorID) error {
	m := l.lockFor(a)
	m.Lock()
	defer m.Unlock()
	return os.RemoveAll(l.dir(a))
}

// Wait blocks until all in-flight invocations complete.
func (l *Local) Wait() { l.wg.Wait() }
