package protohcl

import (
	"fmt"

	hcl "github.com/hashicorp/hcl/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

type DynamicProto struct {
	files *protoregistry.Files
}

// NewDynamicProto parses a protobuf file descriptor set discovered at runtime
// and returns an object which can decode HCL into named message types
// described in that set of files.
//
// This allows a plugin server to dynamically describe its expected schema
// to a plugin client, without the client needing to have protobuf stub code
// pre-generated at compile time. The plugin server can therefore define its
// own message types to use as a way to describe expected HCL structure.
//
// Note that we need a whole file descriptor set rather than just an individual
// message descriptor, because message descriptors can refer to one another,
// potentially across files. The schema description for a plugin might therefore
// be quite large and thus potentially worth caching to use many times, rather
// than repeatedly fetching from the same plugin. A plugin could reduce this
// by using a segregated .proto file just for its configuration-related message
// types, and send only its descriptor over the wire.
func NewDynamicProto(descs *descriptorpb.FileDescriptorSet) (DynamicProto, error) {
	files, err := protodesc.NewFiles(descs)
	if err != nil {
		return DynamicProto{}, fmt.Errorf("invalid descriptors: %w", err)
	}
	return DynamicProto{files}, nil
}

// DecodeBody decodes the content of a given HCL body into a protobuf message
// conforming to the descriptor of the given named message type in the
// dynamically-loaded schema.
func (dp DynamicProto) DecodeBody(body hcl.Body, msgName protoreflect.FullName, ctx *hcl.EvalContext) (proto.Message, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	desc, err := dp.GetMessageDesc(msgName)
	if err != nil {
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Invalid protobuf message type",
			Detail:   fmt.Sprintf("Can't decode into invalid message type %s: %s. This is an internal bug, not a configuration error.", msgName, err),
		})
		return nil, diags
	}

	return DecodeBody(body, desc, ctx)
}

// GetMessageDesc tries to find a message descriptor of the given name in the
// dynamic schema represented by the reciever.
//
// You can pass the result to package function DecodeBody to decode into a
// message of that type, but for most cases it'll be easier to use method
// DynamicProto.DecodeBody, which is a convenience wrapper around these two.
func (dp DynamicProto) GetMessageDesc(name protoreflect.FullName) (protoreflect.MessageDescriptor, error) {
	desc, err := dp.files.FindDescriptorByName(name)
	if err != nil {
		return nil, err
	}

	msgDesc, ok := desc.(protoreflect.MessageDescriptor)
	if !ok {
		return nil, fmt.Errorf("name %s does not refer to a message type", name)
	}

	return msgDesc, nil
}
