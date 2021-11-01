package main

import (
	"context"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/apparentlymart/go-protohcl/examples/rpcplugin/pluginapiproto"
	"github.com/apparentlymart/go-protohcl/protohcl"
	"github.com/apparentlymart/go-protohcl/protohcl/protohclext"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsimple"
	"github.com/zclconf/go-cty-debug/ctydebug"
	"github.com/zclconf/go-cty/cty"
	"go.rpcplugin.org/rpcplugin"
	"go.rpcplugin.org/rpcplugin/plugintrace"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
)

// knownProtoFileDescs is a set of proto files the client just inherently
// knows about, and so the server doesn't need to include these when it
// sends us its own descriptors. (A real application implementation might
// include a negotiation mechanism in its protocol where the client sends
// the server the filenames it knows, and then the server can filter those
// dynamically. But for simpler cases like this one, we'll just assume this
// particular set is defined as part of the RPC protocol.)
var knownProtoFileDescs = []*descriptorpb.FileDescriptorProto{
	protodesc.ToFileDescriptorProto(descriptorpb.File_google_protobuf_descriptor_proto),
	protodesc.ToFileDescriptorProto(anypb.File_google_protobuf_any_proto),
	protodesc.ToFileDescriptorProto(protohclext.File_hcl_proto),
}

func main() {
	logger := log.New(os.Stderr, "client: ", log.Flags())
	ctx := plugintrace.WithClientTracer(context.Background(), plugintrace.ClientLogTracer(logger))

	if len(os.Args) < 2 {
		log.Fatalf("Usage: protohcl-plugin-client CONFIG-FILE")
	}
	configFilename := os.Args[1]

	var mainConfig Config
	err := hclsimple.DecodeFile(configFilename, nil, &mainConfig)
	if err != nil {
		log.Fatalf("failed to read config file: %s", err)
	}

	// The following shows all the low-level machinery of launching and
	// interacting with a plugin, just to show clearly what the steps are.
	// In a real application this would typically be factored out into a
	// helper package.

	// We'll start by launching the plugin server. This expects to find
	// the executable "protohcl-plugin-server" in your PATH, which you can
	// achieve by "go install"ing the server package and making sure your
	// GOBIN directory is in your PATH.
	plugin, err := rpcplugin.New(ctx, &rpcplugin.ClientConfig{
		Handshake: rpcplugin.HandshakeConfig{
			// The client and server must both agree on the CookieKey and
			// CookieValue so that the server can detect whether it's running
			// as a child process of its expected client. If not, it will
			// produce an error message an exit immediately.
			CookieKey:   "PROTOHCL_EXAMPLE_PLUGIN_COOKIE",
			CookieValue: "e8f9c7d7-20fd-55c7-83f9-bee91db2922c",
		},

		ProtoVersions: map[int]rpcplugin.ClientVersion{
			1: protocolVersion1{},
		},

		Cmd:    exec.Command("protohcl-plugin-server"),
		Stderr: os.Stderr, // The two processes can just share our stderr here
	})
	if err != nil {
		logger.Fatalf("failed to start plugin: %s", err)
	}

	protoVersion, clientRaw, err := plugin.Client(ctx)
	if err != nil {
		logger.Fatalf("failed to create plugin client: %s", err)
	}
	if protoVersion != 1 {
		logger.Fatalf("server selected unsupported protocol version %d", protoVersion)
	}
	client := clientRaw.(pluginapiproto.PluginClient)

	// "client" is now an API client for our example application's particular
	// API, as defined in pluginapiproto.

	descResp, err := client.GetConfigDescriptors(ctx, &emptypb.Empty{})
	if err != nil {
		logger.Fatalf("failed to read configuration descriptors: %s", err)
	}

	// We add some common extra files ourselves so that the server doesn't
	// need to send us descriptors we already know.
	descResp.Files.File = append(descResp.Files.File, knownProtoFileDescs...)

	dynProto, err := protohcl.NewDynamicProto(descResp.Files)
	if err != nil {
		logger.Fatalf("failed to process configuration descriptors: %s", err)
	}
	configMsgName := protoreflect.FullName(descResp.ConfigMessageType)
	if !configMsgName.IsValid() {
		logger.Fatalf("invalid config_message_type")
	}

	// We don't really actually need to access the descriptor in here but
	// we'll use this just to show how we might check that it's a valid name.
	_, err = dynProto.GetMessageDesc(configMsgName)
	if err != nil {
		logger.Fatalf("failed to load config message type %s: %s", configMsgName, err)
	}

	// We should now have what we need to decode the plugin-specific
	// configuration block.
	configMsg, diags := dynProto.DecodeBody(mainConfig.Plugin.Raw, configMsgName, nil)
	if diags.HasErrors() {
		logger.Fatalf("invalid config for plugin: %s", diags.Error())
	}

	log.Printf("plugin configuration message is:\n%s", prototext.Format(configMsg))

	configMsgAny, err := anypb.New(configMsg)
	if err != nil {
		logger.Fatalf("failed to prepare configuration message: %s", err)
	}
	executeResp, err := client.Execute(ctx, &pluginapiproto.ExecuteRequest{
		Config: configMsgAny,
	})
	if err != nil {
		logger.Fatalf("plugin Execute failed: %s", err)
	}

	resultMsgAny := executeResp.Result
	resultMsgTypeName := responseMessageTypeName(resultMsgAny)
	logger.Printf("plugin's result is %s", resultMsgTypeName)
	resultMsgDesc, err := dynProto.GetMessageDesc(resultMsgTypeName)
	if err != nil {
		logger.Fatalf("can't find descriptor for response type %s: %s", resultMsgTypeName, err)
	}
	resultMsg := dynamicpb.NewMessage(resultMsgDesc)
	err = resultMsgAny.UnmarshalTo(resultMsg)
	if err != nil {
		logger.Fatalf("failed tp parse plugin response: %s", err)
	}
	log.Printf("plugin result message is:\n%s", prototext.Format(resultMsg))

	resultVal, err := protohcl.ObjectValueForMessage(resultMsg)
	if err != nil {
		logger.Fatalf("failed to decode plugin response: %s", err)
	}

	logger.Printf("plugin result object: %s", ctydebug.ValueString(resultVal))

	finalVal, diags := mainConfig.Result.Value(&hcl.EvalContext{
		Variables: map[string]cty.Value{
			"plugin": resultVal,
		},
	})
	if diags.HasErrors() {
		logger.Fatalf("failed to evaluate final result: %s", diags.Error())
	}

	logger.Printf("final result value: %s", ctydebug.ValueString(finalVal))

	// Must be sure to close the plugin when we're finished with it, so we
	// don't leave an orphaned child process behind.
	err = plugin.Close()
	if err != nil {
		logger.Printf("failed to close plugin: %s", err)
	}
}

// protocolVersion1 is an implementation of rpcplugin.ClientVersion that implements
// protocol version 1.
type protocolVersion1 struct{}

// protocolVersion1 must implement the rpcplugin.ClientVersion interface
var _ rpcplugin.ClientVersion = protocolVersion1{}

func (p protocolVersion1) ClientProxy(ctx context.Context, conn *grpc.ClientConn) (interface{}, error) {
	return pluginapiproto.NewPluginClient(conn), nil
}

func responseMessageTypeName(any *anypb.Any) protoreflect.FullName {
	if slash := strings.LastIndexByte(any.TypeUrl, '/'); slash >= 0 {
		return protoreflect.FullName(any.TypeUrl[slash+1:])
	}
	return protoreflect.FullName(any.TypeUrl)
}
