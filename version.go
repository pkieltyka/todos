package main

import "runtime/debug"

func runtimeVersion(override string) string {
	info, ok := debug.ReadBuildInfo()
	return buildVersion(override, info, ok)
}
func buildVersion(override string, info *debug.BuildInfo, ok bool) string {
	if override != "" && override != "dev" {
		return override
	}
	if ok && info != nil && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
