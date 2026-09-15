package main

import (
	"context"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type toolResultFixerPlugin struct{}

var _ pluginapi.RequestInterceptor = (*toolResultFixerPlugin)(nil)

func (p *toolResultFixerPlugin) InterceptRequestBeforeAuth(_ context.Context, _ pluginapi.RequestInterceptRequest) (pluginapi.RequestInterceptResponse, error) {
	return pluginapi.RequestInterceptResponse{}, nil
}

func (p *toolResultFixerPlugin) InterceptRequestAfterAuth(_ context.Context, req pluginapi.RequestInterceptRequest) (pluginapi.RequestInterceptResponse, error) {
	if req.ToFormat != "antigravity" || req.RequestedModel != "claude-sonnet-4-6" {
		return pluginapi.RequestInterceptResponse{}, nil
	}

	fixed, changed := fixToolResultPairing(req.Body)
	if !changed {
		return pluginapi.RequestInterceptResponse{}, nil
	}
	return pluginapi.RequestInterceptResponse{Body: fixed}, nil
}
