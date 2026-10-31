package router

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func routeMethods(r *gin.Engine, path string) map[string]bool {
	methods := map[string]bool{}
	for _, route := range r.Routes() {
		if route.Path == path {
			methods[route.Method] = true
		}
	}
	return methods
}

func TestExternalAccountBindIsPostOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r)
	methods := routeMethods(r, "/api/admin/oauth2/bind")
	if !methods["POST"] || len(methods) != 1 {
		t.Fatalf("bind route methods = %v, want only POST", methods)
	}
}
