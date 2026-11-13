package main

import (
	_ "embed"

	"github.com/pkieltyka/todos/internal/cli"
)

//go:embed skills/todos/SKILL.md
var skill []byte

var version = "dev"

func main() { cli.Main(skill, runtimeVersion(version)) }
