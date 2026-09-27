package main

import (
	_ "embed"
	"strings"
)

//go:embed version.txt
var rawServerVersion string

func serverVersion() string { return strings.TrimSpace(rawServerVersion) }
