package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// The app shell must be revalidated on every load: a cached index.html (or
// service worker) keeps a phone on the previous deploy's bundle.
func TestAppShellIsNeverServedFromCacheWithoutRevalidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	SetupStaticRoutes(router, nil)

	for _, path := range []string{"/", "/audio/some-job"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, recorder.Code, path)
		require.Equal(t, "no-cache", recorder.Header().Get("Cache-Control"), path)
	}
}
