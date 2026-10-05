package main

import "uniclog.io/govts/internal/appversion"

func applicationVersion() string {
	return appversion.ClientVersion
}
