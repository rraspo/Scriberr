package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

// uploadedForm runs UploadFile against a fake server and returns the
// multipart fields it received.
func uploadedForm(t *testing.T, source string) map[string][]string {
	t.Helper()
	var received map[string][]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(1<<20))
		received = r.MultipartForm.Value
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	viper.Set("server_url", server.URL)
	viper.Set("token", "test-token")
	t.Cleanup(viper.Reset)

	audio := filepath.Join(t.TempDir(), "voice-note.ogg")
	require.NoError(t, os.WriteFile(audio, []byte("audio"), 0o600))

	require.NoError(t, UploadFile(audio, source))
	return received
}

func TestUploadFileSendsTheWatcherSource(t *testing.T) {
	form := uploadedForm(t, "whatsapp")

	require.Equal(t, []string{"whatsapp"}, form["source"])
	require.Equal(t, []string{"voice-note.ogg"}, form["title"])
}

// Without a source the server's default ('manual') applies.
func TestUploadFileOmitsAnEmptySource(t *testing.T) {
	form := uploadedForm(t, "")

	require.NotContains(t, form, "source")
}
