package console

import "embed"

//go:embed templates/*.html static/*
var consoleAssets embed.FS
