// Package kube talks to whatever cluster the user's kubeconfig points
// at: plain namespaced objects, create-or-update, no CRDs and no
// controller.
package kube

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"

	"github.com/gambtho/tomte/tomtectl/internal/manifest"
)

type Client struct {
	cs kubernetes.Interface
	// Namespace is the flag override, else the kubeconfig context's
	// namespace, else "default".
	Namespace string
}

// New builds a client from the user's kubeconfig, honoring the usual
// loading rules (KUBECONFIG, ~/.kube/config).
func New(kubeContext, namespace string) (*Client, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{CurrentContext: kubeContext}
	cfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)

	restCfg, err := cfg.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}
	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, err
	}
	if namespace == "" {
		// A context with no namespace legitimately means "default"; a
		// kubeconfig we cannot read does not, and deploying into the
		// wrong namespace on a swallowed error would be worse than
		// stopping.
		namespace, _, err = cfg.Namespace()
		if err != nil {
			return nil, fmt.Errorf("resolving namespace from kubeconfig: %w", err)
		}
		if namespace == "" {
			namespace = "default"
		}
	}
	return &Client{cs: cs, Namespace: namespace}, nil
}

// Apply creates or updates the agent's ConfigMap and Deployment.
// Updates mutate the freshly-fetched object under RetryOnConflict so
// a concurrent writer (the deployment controller, another tomtectl)
// costs a retry, not a failure.
func (c *Client) Apply(ctx context.Context, cm *corev1.ConfigMap, dep *appsv1.Deployment) error {
	name := cm.Labels[manifest.AgentLabel]
	cms := c.cs.CoreV1().ConfigMaps(c.Namespace)
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := cms.Get(ctx, cm.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = cms.Create(ctx, cm, metav1.CreateOptions{})
			return err
		}
		if err != nil {
			return err
		}
		if err := owned(existing.Labels, name, "ConfigMap", cm.Name); err != nil {
			return err
		}
		existing.Labels = cm.Labels
		existing.Data = cm.Data
		_, err = cms.Update(ctx, existing, metav1.UpdateOptions{})
		return err
	})
	if err != nil {
		return fmt.Errorf("applying ConfigMap: %w", err)
	}

	deps := c.cs.AppsV1().Deployments(c.Namespace)
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := deps.Get(ctx, dep.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = deps.Create(ctx, dep, metav1.CreateOptions{})
			return err
		}
		if err != nil {
			return err
		}
		if err := owned(existing.Labels, name, "Deployment", dep.Name); err != nil {
			return err
		}
		existing.Labels = dep.Labels
		existing.Spec = dep.Spec
		// A ConfigMap-only change does not roll pods by itself; stamp
		// the pod template (kubectl rollout restart's mechanism) so the
		// running agent picks up the new file.
		if existing.Spec.Template.Annotations == nil {
			existing.Spec.Template.Annotations = map[string]string{}
		}
		existing.Spec.Template.Annotations["tomte.dev/restarted-at"] = time.Now().UTC().Format(time.RFC3339)
		_, err = deps.Update(ctx, existing, metav1.UpdateOptions{})
		return err
	})
	if err != nil {
		return fmt.Errorf("applying Deployment: %w", err)
	}
	return nil
}

// ApplySecret creates or updates the Secret holding an agent's API
// key, under the same ownership rule as every other object: a
// same-name Secret that tomtectl does not manage for this agent is
// refused, never overwritten.
func (c *Client) ApplySecret(ctx context.Context, s *corev1.Secret) error {
	agent := s.Labels[manifest.AgentLabel]
	secrets := c.cs.CoreV1().Secrets(c.Namespace)
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := secrets.Get(ctx, s.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = secrets.Create(ctx, s, metav1.CreateOptions{})
			return err
		}
		if err != nil {
			return err
		}
		if err := owned(existing.Labels, agent, "Secret", s.Name); err != nil {
			return err
		}
		existing.Labels = s.Labels
		existing.Data = s.Data
		_, err = secrets.Update(ctx, existing, metav1.UpdateOptions{})
		return err
	})
	if err != nil {
		return fmt.Errorf("applying Secret: %w", err)
	}
	return nil
}

// Status prints deployment readiness and the agent's pods.
func (c *Client) Status(ctx context.Context, name string, out io.Writer) error {
	dep, err := c.cs.AppsV1().Deployments(c.Namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		fmt.Fprintf(out, "agent %q is not deployed in namespace %q — run `tomtectl up`\n", name, c.Namespace)
		return nil
	}
	if err != nil {
		return err
	}
	if err := owned(dep.Labels, name, "Deployment", name); err != nil {
		return err
	}
	fmt.Fprintf(out, "agent %s (namespace %s): %d/%d ready\n",
		name, c.Namespace, dep.Status.ReadyReplicas, dep.Status.Replicas)

	pods, err := c.pods(ctx, name)
	if err != nil {
		return err
	}
	for _, p := range pods {
		restarts := int32(0)
		reason := ""
		for _, cs := range p.Status.ContainerStatuses {
			restarts += cs.RestartCount
			// Surface why a container is stuck (ImagePullBackOff,
			// CreateContainerConfigError, CrashLoopBackOff) — a bare
			// phase reads healthier than it is.
			if w := cs.State.Waiting; w != nil && w.Reason != "" {
				reason = " (" + w.Reason + ")"
			}
		}
		fmt.Fprintf(out, "  pod %s: %s%s, %d restart(s), started %s\n",
			p.Name, p.Status.Phase, reason, restarts, p.CreationTimestamp.Format(time.RFC3339))
	}
	return nil
}

// Logs streams the newest pod's logs.
func (c *Client) Logs(ctx context.Context, name string, follow bool, out io.Writer) error {
	pods, err := c.pods(ctx, name)
	if err != nil {
		return err
	}
	if len(pods) == 0 {
		return fmt.Errorf("no pods for agent %q in namespace %q — run `tomtectl up` and check `tomtectl status`", name, c.Namespace)
	}
	// Prefer the newest Running pod — during a rollout the newest by
	// timestamp can still be Pending, with no logs to stream yet.
	pod := pods[len(pods)-1]
	for i := len(pods) - 1; i >= 0; i-- {
		if pods[i].Status.Phase == corev1.PodRunning {
			pod = pods[i]
			break
		}
	}
	req := c.cs.CoreV1().Pods(c.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Follow: follow})
	stream, err := req.Stream(ctx)
	if err != nil {
		return fmt.Errorf("streaming logs from %s: %w", pod.Name, err)
	}
	defer stream.Close()
	_, err = io.Copy(out, stream)
	return err
}

// Delete removes the agent's objects. Missing objects are tolerated,
// but the caller is told whether anything actually existed — "removed"
// on a wrong namespace would leave the real agent running. Only
// objects carrying tomtectl's ownership labels are deleted, and the
// fetched UID is a delete precondition so a same-name replacement
// created between Get and Delete survives.
func (c *Client) Delete(ctx context.Context, name string) (bool, error) {
	removed := false
	dep, err := c.cs.AppsV1().Deployments(c.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		if err := owned(dep.Labels, name, "Deployment", name); err != nil {
			return removed, err
		}
		opts := metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &dep.UID}}
		err = c.cs.AppsV1().Deployments(c.Namespace).Delete(ctx, name, opts)
		if err == nil {
			removed = true
		} else if !apierrors.IsNotFound(err) {
			return removed, fmt.Errorf("deleting Deployment: %w", err)
		}
	} else if !apierrors.IsNotFound(err) {
		return removed, fmt.Errorf("reading Deployment: %w", err)
	}
	cm, err := c.cs.CoreV1().ConfigMaps(c.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		if err := owned(cm.Labels, name, "ConfigMap", name); err != nil {
			return removed, err
		}
		opts := metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &cm.UID}}
		err = c.cs.CoreV1().ConfigMaps(c.Namespace).Delete(ctx, name, opts)
		if err == nil {
			removed = true
		} else if !apierrors.IsNotFound(err) {
			return removed, fmt.Errorf("deleting ConfigMap: %w", err)
		}
	} else if !apierrors.IsNotFound(err) {
		return removed, fmt.Errorf("reading ConfigMap: %w", err)
	}
	return removed, nil
}

// owned rejects a same-name object that tomtectl does not manage —
// mutating or deleting a bystander would be far worse than stopping.
func owned(labels map[string]string, agent, kind, objName string) error {
	if labels[manifest.AgentLabel] == agent && labels[manifest.ManagedByLabel] == manifest.ManagedByValue {
		return nil
	}
	return fmt.Errorf("a %s named %q exists but is not managed by tomtectl for agent %q — refusing to touch it", kind, objName, agent)
}

func (c *Client) pods(ctx context.Context, name string) ([]corev1.Pod, error) {
	list, err := c.cs.CoreV1().Pods(c.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: manifest.AgentLabel + "=" + name +
			"," + manifest.ManagedByLabel + "=" + manifest.ManagedByValue,
	})
	if err != nil {
		return nil, err
	}
	pods := list.Items
	sort.Slice(pods, func(i, j int) bool {
		if pods[i].CreationTimestamp.Equal(&pods[j].CreationTimestamp) {
			return pods[i].Name < pods[j].Name
		}
		return pods[i].CreationTimestamp.Before(&pods[j].CreationTimestamp)
	})
	return pods, nil
}
