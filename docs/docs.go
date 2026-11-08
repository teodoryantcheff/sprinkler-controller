// Package docs embeds the user-facing documentation into the binary.
package docs

import _ "embed"

// API is the HTTP API reference (API.md), served at /api/docs.
//
//go:embed API.md
var API []byte
