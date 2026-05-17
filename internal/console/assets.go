package console

import "embed"

//go:embed templates/*.html static/console.css
var consoleAssets embed.FS