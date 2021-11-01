package main

import "github.com/hashicorp/hcl/v2"

type Config struct {
	Plugin *PluginConfig  `hcl:"plugin,block"`
	Result hcl.Expression `hcl:"result,attr"`
}

type PluginConfig struct {
	Raw hcl.Body `hcl:",remain"`
}
