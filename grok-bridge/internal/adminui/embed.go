// Package adminui embeds the admin SPA static assets.
package adminui

import "embed"

// Static holds the embedded admin UI files under static/.
//
//go:embed static/*
var Static embed.FS
