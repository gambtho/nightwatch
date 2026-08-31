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
	// AgentLabel marks every object tomtectl manages for an agent.
	AgentLabel = "tomte.dev/agent"
	managedBy  = "app.kubernetes.io/managed-by"

	// Image is the K1 placeholder runtime: a stock shell image running
	// RunScript. K2 replaces it with the real Tomte agent runtime; the
	// mounted agent.yaml contract stays.
	Image = "busybox:1.37.0"

	mountPath = "/tomte"
)

// RunScript is the K1 placeholder runtime, shipped in the ConfigMap
// next to agent.yaml — never baked into an image. It reads the steps
// and schedule from the mounted file at runtime, so editing the
// ConfigMap changes the agent with no CLI involved. Its field
// extraction is deliberately scoped to K1's flat scalars; the CLI's
// strict parser is the real validation gate.
const RunScript = `#!/bin/sh
# Tomte K1 placeholder runtime: prints each step's text on the
# schedule. Replaced by the real agent runtime image in K2. Behavior
# comes from the mounted agent.yaml, never from this image; steps and
# schedule are re-read each wake, so a ConfigMap edit takes effect on
# the next iteration once the volume syncs. A missing file or missing
# schedule is a loud crash, never a silent default.
set -eu
FILE=` + mountPath + `/agent.yaml
read_every() {
  every=$(sed -n 's/^[[:space:]]*every:[[:space:]]*//p' "$FILE" | head -n1 | tr -d '"')
  if [ -z "$every" ]; then
    echo "tomte agent: no schedule.every found in $FILE" >&2
    exit 1
  fi
}
[ -f "$FILE" ] || { echo "tomte agent: $FILE is not mounted" >&2; exit 1; }
read_every
echo "tomte agent starting: waking every $every"
while true; do
  sed -n 's/^[[:space:]]*text:[[:space:]]*//p' "$FILE" | while IFS= read -r line; do
    case "$line" in
      \"*\") line=${line#\"}; line=${line%\"} ;;
    esac
    echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) $line"
  done
  read_every
  sleep "$every"
done
`

// Labels returns the label set shared by every object of an agent.
func Labels(name string) map[string]string {
	return map[string]string{
		AgentLabel: name,
		managedBy:  "tomtectl",
	}
}

// Objects derives the ConfigMap and Deployment for an agent. raw is
// the agent.yaml exactly as the user wrote it — comments included —
// so the in-cluster copy stays the readable artifact.
func Objects(a *agentfile.Agent, raw []byte) (*corev1.ConfigMap, *appsv1.Deployment) {
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
			"run.sh":     RunScript,
		},
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
						Name:    "agent",
						Image:   Image,
						Command: []string{"/bin/sh", mountPath + "/run.sh"},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "agent-file",
							MountPath: mountPath,
							ReadOnly:  true,
						}},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("10m"),
								corev1.ResourceMemory: resource.MustParse("16Mi"),
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
