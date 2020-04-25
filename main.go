package main // github.com/ruinnel/dkron-executor-kubejob

import (
	dkplugin "github.com/distribworks/dkron/v2/plugin"
	"github.com/hashicorp/go-plugin"
)

func main() {
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: dkplugin.Handshake,
		Plugins: map[string]plugin.Plugin{
			dkplugin.ExecutorPluginName: &dkplugin.ExecutorPlugin{Executor: &KubeJob{}},
		},

		// A non-nil value here enables gRPC serving for this plugin...
		GRPCServer: plugin.DefaultGRPCServer,
	})
}
