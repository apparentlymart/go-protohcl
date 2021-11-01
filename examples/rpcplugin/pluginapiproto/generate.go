package pluginapiproto

//go:generate protoc --go_out=. -I../../../schema -I. --go_opt=paths=source_relative,plugins=grpc pluginapi.proto
