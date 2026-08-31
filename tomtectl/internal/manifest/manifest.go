// Package manifest derives the Kubernetes objects for an agent from
// its agent.yaml. The YAML is the single source of truth: nobody
// hand-edits these objects, and the running pod consumes the file
// itself, mounted from the ConfigMap.
package manifest

import (
	"maps"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gambtho/tomte/tomtectl/internal/agentfile"
)

const (
	// AgentLabel marks every object tomtectl manages for an agent; the
	// ManagedBy pair is the ownership check that keeps tomtectl from
	// ever mutating a same-name bystander object.
	AgentLabel     = "tomte.dev/agent"
	ManagedByLabel = "app.kubernetes.io/managed-by"
	ManagedByValue = "tomtectl"

	// DefaultImage is the Tomte agent runtime (cmd/agent-runtime),
	// which reads the mounted agent.yaml each wake and either prints
	// the steps (no llm) or hands them to the configured model. No
	// registry publishes it yet: build it locally and, for kind,
	// `kind load docker-image` it — `up --image` overrides.
	DefaultImage = "tomte-agent:0.2.0"

	// SecretKey is the one key inside the Secret named by
	// spec.llm.secretRef; APIKeyEnv is where the runtime finds it.
	SecretKey = "api_key"
	APIKeyEnv = "TOMTE_API_KEY"

	mountPath = "/tomte"
)

// Labels returns the label set shared by every object of an agent.
func Labels(name string) map[string]string {
	return map[string]string{
		AgentLabel:     name,
		ManagedByLabel: ManagedByValue,
	}
}

// Objects derives the ConfigMap and Deployment for an agent. raw is
// the agent.yaml exactly as the user wrote it — comments included —
// so the in-cluster copy stays the readable artifact.
func Objects(a *agentfile.Agent, raw []byte, image string) (*corev1.ConfigMap, *appsv1.Deployment) {
	name := a.Metadata.Name
	// User labels from the file propagate; tomtectl's own labels win on
	// collision. The Deployment selector stays AgentLabel alone — it is
	// immutable and must not depend on user-editable labels.
	labels := map[string]string{}
	maps.Copy(labels, a.Metadata.Labels)
	maps.Copy(labels, Labels(name))

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Data: map[string]string{
			"agent.yaml": string(raw),
		},
	}

	// The API key reaches the runtime as env drawn from the Secret the
	// file names — it never touches the ConfigMap or the file itself.
	var env []corev1.EnvVar
	if ref := a.Spec.LLM.SecretRef; ref != "" {
		env = append(env, corev1.EnvVar{
			Name: APIKeyEnv,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: ref},
					Key:                  SecretKey,
				},
			},
		})
	}

	one := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{AgentLabel: name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:            "agent",
						Image:           image,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Env:             env,
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "agent-file",
							MountPath: mountPath,
							ReadOnly:  true,
						}},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("10m"),
								corev1.ResourceMemory: resource.MustParse("32Mi"),
							},
						},
					}},
					Volumes: []corev1.Volume{{
						Name: "agent-file",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: name},
							},
						},
					}},
				},
			},
		},
	}
	return cm, dep
}

// SecretForKey builds the Secret `tomtectl set-key` writes: the one
// place the API key lives in the cluster.
func SecretForKey(name, agent string, key []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: Labels(agent)},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{SecretKey: key},
	}
}
