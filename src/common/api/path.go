// Package api provides shared API path construction for all binaries.
package api

import "strings"

const version = "v1"

// APIBasePath returns the base path for the current public API version.
func APIBasePath() string {
	return "/api/" + version
}

// APIPath joins a resource path to the current versioned API base path.
func APIPath(resource string) string {
	return APIBasePath() + "/" + strings.TrimLeft(resource, "/")
}
