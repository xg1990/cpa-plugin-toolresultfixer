package main

import (
	"context"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestInterceptRequestBeforeAuth_PassesThroughWhenUnchanged(t *testing.T) {
	p := &toolResultFixerPlugin{}
	body := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)

	resp, err := p.InterceptRequestBeforeAuth(context.Background(), pluginapi.RequestInterceptRequest{Body: body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Body != nil {
		t.Fatalf("expected an empty Body field so the host leaves the original bytes untouched, got %q", resp.Body)
	}
}

func TestInterceptRequestBeforeAuth_ReturnsFixedBodyWhenChanged(t *testing.T) {
	p := &toolResultFixerPlugin{}
	body := []byte(`{"messages":[
		{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"a","input":{}}]}
	]}`)

	resp, err := p.InterceptRequestBeforeAuth(context.Background(), pluginapi.RequestInterceptRequest{Body: body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Body) == 0 {
		t.Fatalf("expected a non-empty fixed Body when a synthetic tool_result had to be backfilled")
	}
}

func TestInterceptRequestAfterAuth_IsAlwaysANoOp(t *testing.T) {
	p := &toolResultFixerPlugin{}
	body := []byte(`{"messages":[
		{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"a","input":{}}]}
	]}`)

	resp, err := p.InterceptRequestAfterAuth(context.Background(), pluginapi.RequestInterceptRequest{Body: body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Body != nil {
		t.Fatalf("expected InterceptRequestAfterAuth to never touch the body, got %q", resp.Body)
	}
}
