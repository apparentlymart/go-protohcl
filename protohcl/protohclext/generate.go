package protohclext

//go:generate protoc --go_out=. -I../../schema --go_opt=paths=source_relative hcl.proto
