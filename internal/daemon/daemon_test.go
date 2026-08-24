package daemon

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/Mathias-g/Servitor/internal/protocol"
)

func TestRefusesNonLoopback(t *testing.T) {
	if err := checkLoopback("0.0.0.0:7365"); !errors.Is(err, ErrNonLoopback) {
		t.Fatalf("checkLoopback(0.0.0.0:7365) = %v, want ErrNonLoopback", err)
	}
	if err := checkLoopback("10.0.0.1:7365"); !errors.Is(err, ErrNonLoopback) {
		t.Fatalf("checkLoopback(10.0.0.1:7365) = %v, want ErrNonLoopback", err)
	}
}

func TestAllowsLoopback(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:7365", "localhost:7365", "[::1]:7365"} {
		if err := checkLoopback(addr); err != nil {
			t.Errorf("checkLoopback(%q) = %v, want nil", addr, err)
		}
	}
}

func TestRunRefusesNonLoopback(t *testing.T) {
	err := Run(context.Background(), Config{Addr: "0.0.0.0:0"})
	if !errors.Is(err, ErrNonLoopback) {
		t.Fatalf("Run with non-loopback addr = %v, want ErrNonLoopback", err)
	}
}

func TestHealthAndStop(t *testing.T) {
	cfg := Config{Addr: "127.0.0.1:0", DrainTimeout: time.Second}
	srv := NewServer(cfg)
	lis, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(lis) }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := protocol.NewClient(lis.Addr().String())

	if err := c.Health(ctx); err != nil {
		t.Fatalf("health: %v", err)
	}

	if err := c.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned after stop with error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not shut down after /stop")
	}
}
