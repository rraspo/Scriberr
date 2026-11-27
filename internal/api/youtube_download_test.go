package api

import (
	"bytes"
	stdcontext "context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scriberr/internal/config"
	"scriberr/internal/models"
	"scriberr/internal/queue"
	"scriberr/internal/repository"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// fakeYtDlpScript stands in for the yt-dlp binary. FAKE_YTDLP_MODE selects
// the outcome. A metadata call (--print title --print channel) prints the
// title and channel on two lines ("NA" for the channel in mode "nochannel",
// as yt-dlp does for a missing field); a download writes an mp3 at the
// --output template.
const fakeYtDlpScript = `#!/bin/sh
mode="${FAKE_YTDLP_MODE:-ok}"
case "$mode" in
  unavailable)
    echo "ERROR: [youtube] abc123def45: Video unavailable. This video is private" >&2
    exit 1 ;;
  blocked)
    echo "ERROR: [youtube] abc123def45: Sign in to confirm you're not a bot. Use --cookies-from-browser or --cookies for the authentication." >&2
    exit 1 ;;
  broken)
    echo "ERROR: something unexpected happened" >&2
    exit 1 ;;
esac
output=""
previous=""
for argument in "$@"; do
  if [ "$argument" = "--print" ]; then
    echo "Fake Video Title"
    if [ "$mode" = "nochannel" ]; then echo "NA"; else echo "Fake Channel"; fi
    exit 0
  fi
  if [ "$previous" = "--output" ] || [ "$previous" = "-o" ]; then
    output="$argument"
  fi
  previous="$argument"
done
if [ -n "$output" ]; then
  target=$(echo "$output" | sed 's/%(ext)s/mp3/')
  printf 'ID3fake' > "$target"
fi
exit 0
`

func installFakeYtDlp(t *testing.T, mode string) {
	t.Helper()
	binDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "yt-dlp"), []byte(fakeYtDlpScript), 0o755))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_YTDLP_MODE", mode)
}

func newYouTubeTestHandler(t *testing.T) (*Handler, *gorm.DB, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	t.Cleanup(func() {
		if sqlDB, closeErr := db.DB(); closeErr == nil {
			_ = sqlDB.Close()
		}
	})
	require.NoError(t, db.AutoMigrate(&models.TranscriptionProfile{}, &models.TranscriptionJob{}))
	uploadDir := t.TempDir()
	jobRepo := repository.NewJobRepository(db)
	return &Handler{
		config:      &config.Config{UploadDir: uploadDir},
		jobRepo:     jobRepo,
		profileRepo: repository.NewProfileRepository(db),
		taskQueue:   queue.NewTaskQueue(1, nil, jobRepo),
	}, db, uploadDir
}

func createDefaultYouTubeProfile(t *testing.T, db *gorm.DB) *models.TranscriptionProfile {
	t.Helper()
	profile := &models.TranscriptionProfile{
		ID:        "default-profile",
		Name:      "Default",
		IsDefault: true,
		Parameters: models.WhisperXParams{
			Model:    "large-v3",
			Device:   "cuda",
			Language: stringPointer("es"),
			Diarize:  true,
		},
	}
	require.NoError(t, db.Create(profile).Error)
	return profile
}

func stringPointer(value string) *string {
	return &value
}

func performYouTubeRequest(t *testing.T, handler *Handler, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/v1/transcription/youtube", bytes.NewReader(encoded))
	context.Request.Header.Set("Content-Type", "application/json")
	handler.DownloadFromYouTube(context)
	return recorder
}

type youTubeErrorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func decodeYouTubeError(t *testing.T, recorder *httptest.ResponseRecorder) youTubeErrorBody {
	t.Helper()
	var body youTubeErrorBody
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body), recorder.Body.String())
	require.NotEmpty(t, body.Error)
	return body
}

func countJobs(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(&models.TranscriptionJob{}).Count(&count).Error)
	return count
}

func TestYouTubeDownloadQueuesJobWithDefaultProfileAndYouTubeSource(t *testing.T) {
	installFakeYtDlp(t, "ok")
	handler, db, uploadDir := newYouTubeTestHandler(t)
	profile := createDefaultYouTubeProfile(t, db)

	response := performYouTubeRequest(t, handler, map[string]any{"url": "https://www.youtube.com/watch?v=abc123def45"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var returned models.TranscriptionJob
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &returned))
	require.Equal(t, models.StatusPending, returned.Status)

	var stored models.TranscriptionJob
	require.NoError(t, db.First(&stored, "id = ?", returned.ID).Error)
	require.Equal(t, models.StatusPending, stored.Status)
	require.Equal(t, "youtube", stored.Source)
	require.NotNil(t, stored.Title)
	require.Equal(t, "Fake Video Title", *stored.Title)
	require.Equal(t, profile.Parameters.Model, stored.Parameters.Model)
	require.True(t, stored.Diarization)
	require.True(t, strings.HasPrefix(stored.AudioPath, uploadDir), stored.AudioPath)
	require.FileExists(t, stored.AudioPath)
	require.NotNil(t, stored.SourceURL)
	require.Equal(t, "https://www.youtube.com/watch?v=abc123def45", *stored.SourceURL)
	require.NotNil(t, stored.SourceChannel)
	require.Equal(t, "Fake Channel", *stored.SourceChannel)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.Equal(t, "https://www.youtube.com/watch?v=abc123def45", payload["source_url"])
	require.Equal(t, "Fake Channel", payload["source_channel"])
}

func TestYouTubeDownloadStoresTrimmedURLAndSkipsMissingChannel(t *testing.T) {
	installFakeYtDlp(t, "nochannel")
	handler, db, _ := newYouTubeTestHandler(t)
	createDefaultYouTubeProfile(t, db)

	response := performYouTubeRequest(t, handler, map[string]any{"url": "  https://youtu.be/abc123def45  "})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var stored models.TranscriptionJob
	require.NoError(t, db.First(&stored).Error)
	require.NotNil(t, stored.SourceURL)
	require.Equal(t, "https://youtu.be/abc123def45", *stored.SourceURL)
	require.Nil(t, stored.SourceChannel, "yt-dlp's NA placeholder is not a channel name")
	require.Equal(t, "Fake Video Title", *stored.Title)
}

func TestYouTubeDownloadSurvivesClientDisconnect(t *testing.T) {
	installFakeYtDlp(t, "ok")
	handler, db, _ := newYouTubeTestHandler(t)
	createDefaultYouTubeProfile(t, db)

	encoded, err := json.Marshal(map[string]any{"url": "https://www.youtube.com/watch?v=abc123def45"})
	require.NoError(t, err)
	cancelled, cancel := stdcontext.WithCancel(stdcontext.Background())
	cancel()
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/api/v1/transcription/youtube", bytes.NewReader(encoded)).WithContext(cancelled)
	ginContext.Request.Header.Set("Content-Type", "application/json")
	handler.DownloadFromYouTube(ginContext)

	var stored models.TranscriptionJob
	require.NoError(t, db.First(&stored).Error, "a finished download must be kept when the browser goes away")
	require.Equal(t, models.StatusPending, stored.Status)
	require.FileExists(t, stored.AudioPath)
}

func TestYouTubeDownloadKeepsCustomTitle(t *testing.T) {
	installFakeYtDlp(t, "ok")
	handler, db, _ := newYouTubeTestHandler(t)
	createDefaultYouTubeProfile(t, db)

	response := performYouTubeRequest(t, handler, map[string]any{
		"url":   "https://youtu.be/abc123def45",
		"title": "My own title",
	})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var stored models.TranscriptionJob
	require.NoError(t, db.First(&stored).Error)
	require.Equal(t, "My own title", *stored.Title)
	require.Equal(t, "youtube", stored.Source)
}

func TestYouTubeDownloadFallsBackToUploadedWhenEnqueueFails(t *testing.T) {
	installFakeYtDlp(t, "ok")
	handler, db, _ := newYouTubeTestHandler(t)
	createDefaultYouTubeProfile(t, db)
	handler.taskQueue.Stop()

	response := performYouTubeRequest(t, handler, map[string]any{"url": "https://www.youtube.com/watch?v=abc123def45"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var returned models.TranscriptionJob
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &returned))
	require.Equal(t, models.StatusUploaded, returned.Status)

	var stored models.TranscriptionJob
	require.NoError(t, db.First(&stored, "id = ?", returned.ID).Error)
	require.Equal(t, models.StatusUploaded, stored.Status)
	require.Equal(t, "youtube", stored.Source)
	require.NotNil(t, stored.ErrorMessage, "the reason the job was not queued must be visible on the job")
	require.NotEmpty(t, *stored.ErrorMessage)
}

func TestYouTubeDownloadWithoutProfilesStaysUploadedWithReason(t *testing.T) {
	installFakeYtDlp(t, "ok")
	handler, db, _ := newYouTubeTestHandler(t)

	response := performYouTubeRequest(t, handler, map[string]any{"url": "https://www.youtube.com/watch?v=abc123def45"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var stored models.TranscriptionJob
	require.NoError(t, db.First(&stored).Error)
	require.Equal(t, models.StatusUploaded, stored.Status)
	require.NotNil(t, stored.ErrorMessage)
	require.NotEmpty(t, *stored.ErrorMessage)
}

func TestYouTubeDownloadRejectsNonYouTubeURL(t *testing.T) {
	installFakeYtDlp(t, "ok")
	handler, db, _ := newYouTubeTestHandler(t)
	createDefaultYouTubeProfile(t, db)

	for _, rawURL := range []string{
		"https://example.com/watch?v=abc123def45",
		"https://notyoutube.com.example.org/watch?v=abc123def45",
		"https://example.com/?redirect=youtube.com",
		"not a url",
	} {
		response := performYouTubeRequest(t, handler, map[string]any{"url": rawURL})
		require.Equal(t, http.StatusBadRequest, response.Code, rawURL)
		require.Equal(t, "invalid_url", decodeYouTubeError(t, response).Code, rawURL)
	}
	require.Zero(t, countJobs(t, db))
}

func TestYouTubeDownloadReportsWhyTheDownloadFailed(t *testing.T) {
	cases := []struct {
		mode   string
		status int
		code   string
	}{
		{mode: "unavailable", status: http.StatusUnprocessableEntity, code: "video_unavailable"},
		{mode: "blocked", status: http.StatusBadGateway, code: "youtube_blocked"},
		{mode: "broken", status: http.StatusBadGateway, code: "download_failed"},
	}
	for _, testCase := range cases {
		t.Run(testCase.mode, func(t *testing.T) {
			installFakeYtDlp(t, testCase.mode)
			handler, db, uploadDir := newYouTubeTestHandler(t)
			createDefaultYouTubeProfile(t, db)

			response := performYouTubeRequest(t, handler, map[string]any{"url": "https://www.youtube.com/watch?v=abc123def45"})
			require.Equal(t, testCase.status, response.Code, response.Body.String())
			require.Equal(t, testCase.code, decodeYouTubeError(t, response).Code)
			require.Zero(t, countJobs(t, db))

			leftovers, err := os.ReadDir(uploadDir)
			require.NoError(t, err)
			require.Empty(t, leftovers, "a failed download must not leave files behind")
		})
	}
}
