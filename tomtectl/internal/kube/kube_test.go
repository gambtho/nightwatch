package kube

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

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

// TestApplySecretSurvivesCreateRace: a concurrent first write landing
// between Get and Create must cost a retry (refetch → update), not a
// failure.
func TestApplySecretSurvivesCreateRace(t *testing.T) {
	ctx := context.Background()
	cs := fake.NewClientset()
	raced := false
	cs.PrependReactor("create", "secrets", func(action k8stesting.Action) (bool, k8sruntime.Object, error) {
		if raced {
			return false, nil, nil
		}
		raced = true
		// Simulate the racer: its Secret appears in the store, and our
		// Create fails AlreadyExists.
		winner := manifest.SecretForKey("hello-key", "hello", []byte("sk-racer"))
		winner.Namespace = "default"
		if err := cs.Tracker().Add(winner); err != nil {
			t.Fatal(err)
		}
		return true, nil, apierrors.NewAlreadyExists(corev1.Resource("secrets"), "hello-key")
	})
	c := &Client{cs: cs, Namespace: "default"}
	if err := c.ApplySecret(ctx, manifest.SecretForKey("hello-key", "hello", []byte("sk-ours"))); err != nil {
		t.Fatalf("racy create must reconcile, got %v", err)
	}
	got, _ := cs.CoreV1().Secrets("default").Get(ctx, "hello-key", metav1.GetOptions{})
	if string(got.Data[manifest.SecretKey]) != "sk-ours" {
		t.Errorf("retry must apply our key, got %q", got.Data[manifest.SecretKey])
	}
}

func TestSecretExistsValidatesTheKeyEntry(t *testing.T) {
	ctx := context.Background()
	cs := fake.NewClientset()
	c := &Client{cs: cs, Namespace: "default"}

	if ok, err := c.SecretExists(ctx, "absent"); ok || err != nil {
		t.Errorf("absent secret: want (false, nil), got (%v, %v)", ok, err)
	}

	empty := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "no-key"}}
	if _, err := cs.CoreV1().Secrets("default").Create(ctx, empty, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SecretExists(ctx, "no-key"); err == nil || !strings.Contains(err.Error(), "set-key") {
		t.Errorf("secret without api_key must point at set-key, got %v", err)
	}

	good := manifest.SecretForKey("good", "hello", []byte("sk-x"))
	if _, err := cs.CoreV1().Secrets("default").Create(ctx, good, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if ok, err := c.SecretExists(ctx, "good"); !ok || err != nil {
		t.Errorf("good secret: want (true, nil), got (%v, %v)", ok, err)
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
