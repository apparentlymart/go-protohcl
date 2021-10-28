package protohcl

import (
	hcl "github.com/hashicorp/hcl/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// DecodeBody decodes the content of the given body into a message that
// conforms to the given message descriptor.
//
// You can use this method directly only if the caller has access to generated
// stub code for the relevant protobuf schema. If you need to work with
// schemas loaded only at runtime, such as over a plugin wire protocol, use
// DynamicProto instead.
func DecodeBody(body hcl.Body, desc protoreflect.MessageDescriptor) (proto.Message, hcl.Diagnostics) {
	panic("not yet implemented")
}
