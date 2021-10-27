// Package protohcl provides a glue layer between HCL (the configuration
// language toolkit) and protobuf message types.
//
// The primary goal of protohcl is to allow an application which uses both
// HCL as the basis of its configuration language and uses protobuf-based
// plugins (using e.g. rpcplugin or HashiCorp's go-plugin) can let plugins
// describe their intended configuration structure as a protobuf message
// descriptor, and then use protohcl to automatically populate a protobuf
// message conforming to that descriptor to send to the plugin as a
// language-agnostic representation of its configuration. In such a system,
// only the client which decodes the configuration need be written in Go,
// whereas the plugins can be written in any protobuf-supporting language.
//
// protohcl works by defining a small set of protobuf extension options,
// defined in the associated protobuf schema hcl.proto. protohcl then
// detects those options in the given message descriptors and uses them
// to derive an equivalent HCL schema for decoding.
package protohcl
