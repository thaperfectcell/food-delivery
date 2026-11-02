package provider

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSimulatorApprovesWithinLimit(t *testing.T) {
	s := NewSimulator(0, 1_000_000)
	res, err := s.Charge(context.Background(), "order-1", 1297_00)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Approved || !strings.HasPrefix(res.ProviderRef, "SIM-") || len(res.ProviderRef) != 12 {
		t.Fatalf("unexpected result %+v", res)
	}
	again, _ := s.Charge(context.Background(), "order-1", 1297_00)
	if again.ProviderRef != res.ProviderRef {
		t.Fatal("provider must be idempotent by key")
	}
	other, _ := s.Charge(context.Background(), "order-2", 1297_00)
	if other.ProviderRef == res.ProviderRef {
		t.Fatal("different keys must produce different references")
	}
}

func TestSimulatorDeclines(t *testing.T) {
	s := NewSimulator(0, 1_000_000)
	res, _ := s.Charge(context.Background(), "o", 1_000_001)
	if res.Approved || res.DeclineReason != ReasonLimitExceeded {
		t.Fatalf("amount over limit must be declined: %+v", res)
	}
	res, _ = s.Charge(context.Background(), "o", 1_000_000)
	if !res.Approved {
		t.Fatal("amount equal to limit must be approved")
	}
	res, _ = s.Charge(context.Background(), "o", 0)
	if res.Approved || res.DeclineReason != ReasonInvalidAmount {
		t.Fatalf("zero amount must be declined: %+v", res)
	}
}

func TestSimulatorRespectsContext(t *testing.T) {
	s := NewSimulator(time.Hour, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := s.Charge(ctx, "o", 100)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("charge must stop when context is done")
	}
}
