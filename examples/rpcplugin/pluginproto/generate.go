package pluginproto

//go:generate protoc --go_out=. -I../../../schema -I. --go_opt=paths=source_relative plugin.proto
