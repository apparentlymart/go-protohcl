package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"go.rpcplugin.org/rpcplugin"
	"go.rpcplugin.org/rpcplugin/plugintrace"
)

func main() {
	logger := log.New(os.Stderr, "server: ", log.Flags())
	ctx := plugintrace.WithServerTracer(context.Background(), plugintrace.ServerLogTracer(logger))

	err := rpcplugin.Serve(ctx, &rpcplugin.ServerConfig{
		Handshake: rpcplugin.HandshakeConfig{
			// The client and server must both agree on the CookieKey and
			// CookieValue so that the server can detect whether it's running
			// as a child process of its expected client. If not, it will
			// produce an error message an exit immediately.
			CookieKey:   "PROTOHCL_EXAMPLE_PLUGIN_COOKIE",
			CookieValue: "e8f9c7d7-20fd-55c7-83f9-bee91db2922c",
		},
		ProtoVersions: map[int]rpcplugin.ServerVersion{
			1: protocolVersion1{
				logger: logger,
			},
		},
	})

	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
