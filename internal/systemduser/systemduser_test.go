package systemduser

import (
	"context"
	"testing"
)

func TestNewClient(t *testing.T) {
	c := NewClient()
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
}

func TestTryRestartUnits_Empty(t *testing.T) {
	c := NewClient()
	err := c.TryRestartUnits(context.Background(), []string{})
	if err != nil {
		t.Fatalf("TryRestartUnits with empty slice returned error: %v", err)
	}
}

func TestRestartUnits_Empty(t *testing.T) {
	c := NewClient()
	err := c.RestartUnits(context.Background(), []string{})
	if err != nil {
		t.Fatalf("RestartUnits with empty slice returned error: %v", err)
	}
}
