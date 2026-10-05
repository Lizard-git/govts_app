package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"regexp"

	"uniclog.io/govts/internal/appversion"
)

var configVersionPattern = regexp.MustCompile(`(?m)^([ \t]+version:[ \t]*")[^"\r\n]*("[ \t]*\r?$)`)

func main() {
	tag := flag.String("tag", "", "optional release tag to validate against clientVersion")
	wailsConfig := flag.String("wails-config", "", "optional Wails YAML config to synchronize")
	flag.Parse()
	if err := run(*tag, *wailsConfig); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(appversion.ClientVersion)
}

func run(tag, wailsConfig string) error {
	if tag != "" && tag != "v"+appversion.ClientVersion {
		return fmt.Errorf("release tag %q does not match version/release.json clientVersion (%s)", tag, appversion.ClientVersion)
	}
	if wailsConfig == "" {
		return nil
	}
	config, err := os.ReadFile(wailsConfig)
	if err != nil {
		return fmt.Errorf("read Wails config: %w", err)
	}
	if len(configVersionPattern.FindAll(config, -1)) != 1 {
		return fmt.Errorf("Wails config must contain exactly one quoted, indented version field")
	}
	updated := configVersionPattern.ReplaceAll(config, []byte("${1}"+appversion.ClientVersion+"${2}"))
	if bytes.Equal(config, updated) {
		return nil
	}
	if err := os.WriteFile(wailsConfig, updated, 0644); err != nil {
		return fmt.Errorf("write Wails config: %w", err)
	}
	return nil
}
