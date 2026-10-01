package jevmodels

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestReadinessRequiresHealthAndPrivateToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"ok":true,"device":"cpu"}`))
	}))
	defer server.Close()
	p := &modelProcess{endpoint: server.URL, token: "secret", done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := p.waitReady(ctx); err != nil {
		t.Fatal(err)
	}
	if p.device != "cpu" {
		t.Fatal(p.device)
	}
}

func TestReadinessTimesOutForUnhealthyWorker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"ok":false,"device":"cpu"}`)) }))
	defer server.Close()
	p := &modelProcess{endpoint: server.URL, done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if err := p.waitReady(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestReadinessReportsEarlyExit(t *testing.T) {
	p := &modelProcess{done: make(chan struct{}), err: errors.New("worker failed")}
	close(p.done)
	if err := p.waitReady(t.Context()); err == nil {
		t.Fatal("early exit accepted")
	}
}
