package kube

import (
	"strings"
	"testing"

	"github.com/gambtho/tomte/tomtectl/internal/manifest"
)

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
