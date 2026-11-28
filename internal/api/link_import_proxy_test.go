package api

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func readCalls(t *testing.T, logPath string) []string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	require.NoError(t, err)
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// Link imports leave through the configured proxy, so the post sites see the
// proxy's address instead of the host's default route.
func TestLinkImportSendsEveryYtDlpCallThroughTheConfiguredProxy(t *testing.T) {
	callLog := installFakeSocialYtDlp(t, "ig")
	handler, db, _ := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)
	handler.config.LinkImportProxy = "http://egress-proxy.example:3128"

	response := performLinkImport(t, handler, map[string]any{"url": "https://www.instagram.com/reel/ABC123xyz/"})
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	preview := performPreviewRequest(t, handler, "https://www.instagram.com/reel/ABC123xyz/")
	require.Equal(t, http.StatusOK, preview.Code, preview.Body.String())

	calls := readCalls(t, callLog)
	require.Len(t, calls, 3, "metadata, download, preview metadata")
	for _, call := range calls {
		require.Contains(t, call, "--proxy http://egress-proxy.example:3128", call)
	}
}

func TestLinkImportWithoutAProxyRunsYtDlpDirectly(t *testing.T) {
	callLog := installFakeSocialYtDlp(t, "ig")
	handler, db, _ := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)

	response := performLinkImport(t, handler, map[string]any{"url": "https://www.instagram.com/reel/ABC123xyz/"})
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())

	for _, call := range readCalls(t, callLog) {
		require.NotContains(t, call, "--proxy", call)
	}
}
