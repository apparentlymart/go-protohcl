package main

import (
	"context"
	"fmt"
	"log"

	"github.com/apparentlymart/go-protohcl/examples/rpcplugin/pluginapiproto"
	"github.com/apparentlymart/go-protohcl/examples/rpcplugin/pluginproto"
	"go.rpcplugin.org/rpcplugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
)

// plugin1Server is the server implementation of plugin protocol version 1.
type plugin1Server struct {
	logger *log.Logger
}

// plugin1Server must implement the RPC server interface
var _ pluginapiproto.PluginServer = (*plugin1Server)(nil)

func (s *plugin1Server) Execute(ctx context.Context, req *pluginapiproto.ExecuteRequest) (*pluginapiproto.ExecuteResponse, error) {
	config := &pluginproto.Config{}
	err := req.Config.UnmarshalTo(config)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid config message: %s", err)
	}

	name := config.Name
	project := config.Project
	region := config.Region

	if project == "" {
		project = "default-project"
	}
	if region == "" {
		region = "us-west"
	}

	id := fmt.Sprintf("app:%s:%s:%s", region, project, name)

	result := &pluginproto.Result{
		Id: id,
	}

	for _, serviceConfig := range config.Services {
		result.ServiceIds = append(result.ServiceIds, fmt.Sprintf("%s/%s", id, serviceConfig.Name))
	}

	resultAny, err := anypb.New(result)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to encode reponse message: %s", err)
	}

	return &pluginapiproto.ExecuteResponse{
		Result: resultAny,
	}, nil
}

func (s *plugin1Server) GetConfigDescriptors(ctx context.Context, req *emptypb.Empty) (*pluginapiproto.ConfigDescriptors, error) {
	fileDescs := &descriptorpb.FileDescriptorSet{}
	fileDesc := protodesc.ToFileDescriptorProto(pluginproto.File_plugin_proto)
	fileDescs.File = append(fileDescs.File, fileDesc)
	// NOTE: This plugin happens to only need that one file, but other plugins
	// might need to include more than one file if their main file imports
	// others that the client wouldn't know about.

	resp := &pluginapiproto.ConfigDescriptors{
		Files:             fileDescs,
		ConfigMessageType: "protohcl.example.rpcplugin.plugin.Config",
	}
	return resp, nil
}

// protocolVersion1 is an implementation of rpcplugin.ServerVersion that implements
// protocol version 1.
type protocolVersion1 struct {
	logger *log.Logger
}

// protocolVersion1 must implement the rpcplugin.ServerVersion interface
var _ rpcplugin.ServerVersion = protocolVersion1{}

func (p protocolVersion1) RegisterServer(server *grpc.Server) error {
	pluginapiproto.RegisterPluginServer(server, &plugin1Server{logger: p.logger})
	return nil
}
