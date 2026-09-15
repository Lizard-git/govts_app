package main

import (
	_ "embed"
	"strings"
)

//go:embed version.txt
var rawApplicationVersion string

func applicationVersion() string {
	return strings.TrimSpace(rawApplicationVersion)
}
