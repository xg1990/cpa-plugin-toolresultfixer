package main

import (
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const pluginID = "toolresultfixer"

var pluginVersion = "0.1.0"

func buildPlugin() pluginapi.Plugin {
	p := &toolResultFixerPlugin{}
	return pluginapi.Plugin{
		Metadata: pluginapi.Metadata{
			Name:             pluginID,
			Version:          pluginVersion,
			Author:           "xg1990",
			GitHubRepository: "https://github.com/xg1990/cpa-plugin-toolresultfixer",
		},
		Capabilities: pluginapi.Capabilities{
			RequestInterceptor: p,
		},
	}
}
