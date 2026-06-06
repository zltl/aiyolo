package console

import "embed"

//go:embed templates/*.html static/* static/vendor/**
var consoleAssets embed.FS
