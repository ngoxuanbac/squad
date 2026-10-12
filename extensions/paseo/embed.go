// Package paseoext embeds squad's Paseo plugin; `squad setup paseo` writes it out and installs it.
package paseoext

import "embed"

//go:embed paseo-plugin.json OVERVIEW.md package.json tsconfig.json index.client.tsx index.server.ts client server shared
var FS embed.FS

// InstalledFile is the file whose placeholder names the squad binary.
const InstalledFile = "server/installed.ts"

// InstalledPlaceholder is its content in the repository.
const InstalledPlaceholder = `export const squadPath = "";`
