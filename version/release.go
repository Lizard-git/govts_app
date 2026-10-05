// Package version embeds the release configuration shared by all binaries.
package version

import (
	_ "embed"
	"encoding/json"
)

//go:embed release.json
var releaseJSON []byte

type Release struct {
	ClientVersion    string `json:"clientVersion"`
	ServerVersion    string `json:"serverVersion"`
	MinServerVersion string `json:"minServerVersion"`
}

func Read() (Release, error) {
	var release Release
	err := json.Unmarshal(releaseJSON, &release)
	return release, err
}
