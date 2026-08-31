package kube

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/gambtho/tomte/tomtectl/internal/manifest"
)

func TestApplySecretCreatesUpdatesAndRefusesBystanders(t *testing.T) {
	ctx := context.Background()
	cs := fake.NewClientset()
	c := &Client{cs: cs, Namespace: "default"}

	s := manifest.SecretForKey("hello-key", "hello", []byte("sk-one"))
	if err := c.ApplySecret(ctx, s); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.ApplySecret(ctx, manifest.SecretForKey("hello-key", "hello", []byte("sk-two"))); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := cs.CoreV1().Secrets("default").Get(ctx, "hello-key", metav1.GetOptions{})
	if string(got.Data[manifest.SecretKey]) != "sk-two" {
		t.Errorf("re-running set-key must replace the key, got %q", got.Data[manifest.SecretKey])
	}

	bystander := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "their-key"}}
	if _, err := cs.CoreV1().Secrets("default").Create(ctx, bystander, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	err := c.ApplySecret(ctx, manifest.SecretForKey("their-key", "hello", []byte("sk-evil")))
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("bystander Secret must be refused, got %v", err)
	}
}

func TestOwned(t *testing.T) {
	good := manifest.Labels("hello")
	if err := owned(good, "hello", "Deployment", "hello"); err != nil {
		t.Errorf("tomtectl's own labels must pass: %v", err)
	}
	cases := []map[string]string{
		nil,
		{manifest.AgentLabel: "hello"}, // missing managed-by
		{manifest.ManagedByLabel: manifest.ManagedByValue},                               // missing agent label
		{manifest.AgentLabel: "other", manifest.ManagedByLabel: manifest.ManagedByValue}, // another agent's object
	}
	for _, labels := range cases {
		err := owned(labels, "hello", "Deployment", "hello")
		if err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("labels %v: want refusal, got %v", labels, err)
		}
	}
}
