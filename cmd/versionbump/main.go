package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var configVersionPattern = regexp.MustCompile(`(?m)^(\s*version:\s*")[^"]+("\s*)$`)

func main() {
	versionFile := flag.String("version-file", "", "path to the authoritative semantic version file")
	wailsConfig := flag.String("wails-config", "", "optional path to the Wails YAML config")
	flag.Parse()

	if *versionFile == "" {
		fatal(errors.New("-version-file is required"))
	}

	previousBytes, err := os.ReadFile(*versionFile)
	if err != nil {
		fatal(fmt.Errorf("read version file: %w", err))
	}
	previous := strings.TrimSpace(string(previousBytes))
	next, err := bumpPatch(previous)
	if err != nil {
		fatal(err)
	}

	var updatedConfig []byte
	if *wailsConfig != "" {
		configBytes, err := os.ReadFile(*wailsConfig)
		if err != nil {
			fatal(fmt.Errorf("read Wails config: %w", err))
		}
		if !configVersionPattern.Match(configBytes) {
			fatal(errors.New("Wails config does not contain a quoted version field"))
		}
		updatedConfig = configVersionPattern.ReplaceAll(configBytes, []byte("${1}"+next+"${2}"))
	}

	if err := os.WriteFile(*versionFile, []byte(next+"\n"), 0o644); err != nil {
		fatal(fmt.Errorf("write version file: %w", err))
	}
	if *wailsConfig != "" {
		if err := os.WriteFile(*wailsConfig, updatedConfig, 0o644); err != nil {
			_ = os.WriteFile(*versionFile, previousBytes, 0o644)
			fatal(fmt.Errorf("write Wails config: %w", err))
		}
	}

	fmt.Printf("version: %s -> %s\n", previous, next)
}

func bumpPatch(version string) (string, error) {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("version %q must have the form major.minor.patch", version)
	}
	values := make([]uint64, len(parts))
	for i, part := range parts {
		if part == "" {
			return "", fmt.Errorf("version %q must contain only non-negative integers", version)
		}
		value, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return "", fmt.Errorf("version %q must contain only non-negative integers", version)
		}
		values[i] = value
	}
	if values[2] == ^uint64(0) {
		return "", errors.New("patch version overflow")
	}
	values[2]++
	return fmt.Sprintf("%d.%d.%d", values[0], values[1], values[2]), nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "version bump:", err)
	os.Exit(1)
}
