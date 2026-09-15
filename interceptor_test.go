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

func TestInterceptRequestBeforeAuth_PassesThroughWhenChangedPayload(t *testing.T) {
	p := &toolResultFixerPlugin{}
	body := []byte(`{"messages":[
		{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"a","input":{}}]}
	]}`)

	resp, err := p.InterceptRequestBeforeAuth(context.Background(), pluginapi.RequestInterceptRequest{Body: body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Body != nil {
		t.Fatalf("expected BeforeAuth to leave the body untouched, got %q", resp.Body)
	}
}

func TestInterceptRequestAfterAuth_IgnoresNonAntigravity(t *testing.T) {
	p := &toolResultFixerPlugin{}
	body := []byte(`{"messages":[
		{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"a","input":{}}]}
	]}`)

	resp, err := p.InterceptRequestAfterAuth(context.Background(), pluginapi.RequestInterceptRequest{
		Body:           body,
		ToFormat:       "anthropic",
		RequestedModel: "claude-sonnet-4-6",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Body != nil {
		t.Fatalf("expected non-Antigravity request to pass through, got %q", resp.Body)
	}
}

func TestInterceptRequestAfterAuth_IgnoresSonnet5(t *testing.T) {
	p := &toolResultFixerPlugin{}
	body := []byte(`{"messages":[
		{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"a","input":{}}]}
	]}`)

	resp, err := p.InterceptRequestAfterAuth(context.Background(), pluginapi.RequestInterceptRequest{
		Body:           body,
		ToFormat:       "antigravity",
		RequestedModel: "claude-sonnet-5",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Body != nil {
		t.Fatalf("expected Sonnet 5 request to pass through, got %q", resp.Body)
	}
}

func TestInterceptRequestAfterAuth_FixesAntigravitySonnet46(t *testing.T) {
	p := &toolResultFixerPlugin{}
	body := []byte(`{"messages":[
		{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"a","input":{}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","content":"result"}]},
		{"role":"system","content":[{"type":"text","text":"<system-reminder>continue</system-reminder>"}]}
	]}`)

	resp, err := p.InterceptRequestAfterAuth(context.Background(), pluginapi.RequestInterceptRequest{
		Body:           body,
		ToFormat:       "antigravity",
		RequestedModel: "claude-sonnet-4-6",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Body) == 0 {
		t.Fatalf("expected Antigravity Sonnet 4.6 request to be fixed")
	}
	root := decodeForAssertions(t, resp.Body)
	if len(messagesOf(t, root)) != 2 {
		t.Fatalf("expected system reminder to merge into the tool-result user message")
	}
}
