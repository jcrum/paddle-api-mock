package handlers

import (
	"net/http"
	"strings"
)

// resourcePath returns the part of the path after the resource name. Paddle's paths have no version prefix
// (https://api.paddle.com/products); /v1/ is still accepted for existing consumers.
func resourcePath(r *http.Request, resource string) string {
	path := strings.TrimPrefix(r.URL.Path, "/v1")
	path = strings.TrimPrefix(path, "/"+resource)
	return strings.TrimPrefix(path, "/")
}
