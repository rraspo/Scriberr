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
// the outcome. A metadata call (--dump-json) prints the video's info JSON,
// whose language depends on the mode ("ok" is English, "es" Spanish, "fr"
// French, "nolang" has no language and no channel). A download writes an mp3
// at the --output template. Every call is appended to FAKE_YTDLP_LOG.
const fakeYtDlpScript = `#!/bin/sh
mode="${FAKE_YTDLP_MODE:-ok}"
if [ -n "$FAKE_YTDLP_LOG" ]; then echo "$*" >> "$FAKE_YTDLP_LOG"; fi
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
  if [ "$argument" = "--dump-json" ] || [ "$argument" = "-j" ]; then
    case "$mode" in
      es) echo '{"id":"abc123def45","title":"Video en español","channel":"Canal","duration":95,"language":"es"}' ;;
      fr) echo '{"id":"abc123def45","title":"Vidéo","channel":"Chaîne","duration":95,"language":"fr"}' ;;
      nolang) echo '{"id":"abc123def45","title":"Fake Video Title","channel":null,"duration":95,"language":null}' ;;
      *) echo '{"id":"abc123def45","title":"Fake Video Title","channel":"Fake Channel","duration":1427,"language":"en-US","thumbnail":"https://i.ytimg.com/vi/abc123def45/maxresdefault.jpg"}' ;;
    esac
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

// installFakeYtDlp puts the fake on PATH and returns the file its calls are
// logged to.
func installFakeYtDlp(t *testing.T, mode string) string {
	t.Helper()
	binDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "yt-dlp"), []byte(fakeYtDlpScript), 0o755))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_YTDLP_MODE", mode)
	logPath := filepath.Join(binDir, "calls.log")
	t.Setenv("FAKE_YTDLP_LOG", logPath)
	return logPath
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
	handler := &Handler{
		config:      &config.Config{UploadDir: uploadDir},
		jobRepo:     jobRepo,
		profileRepo: repository.NewProfileRepository(db),
		taskQueue:   queue.NewTaskQueue(1, nil, jobRepo),
	}
	// Tests never reach YouTube; by default the thumbnail host has nothing.
	serveThumbnailUpstream(t, handler, http.StatusNotFound)
	return handler, db, uploadDir
}

func createLanguageProfile(t *testing.T, db *gorm.DB, id string, name string, language string, isDefault bool, isFallback bool) *models.TranscriptionProfile {
	t.Helper()
	profile := &models.TranscriptionProfile{
		ID:         id,
		Name:       name,
		IsDefault:  isDefault,
		IsFallback: isFallback,
		Parameters: models.WhisperXParams{
			Model:    "large-v3",
			Device:   "cuda",
			Language: stringPointer(language),
			Diarize:  true,
		},
	}
	require.NoError(t, db.Create(profile).Error)
	return profile
}

// createHouseProfiles mirrors the live setup: a default Spanish GPU profile,
// an English GPU profile, and a fallback profile per language.
func createHouseProfiles(t *testing.T, db *gorm.DB) (spanish *models.TranscriptionProfile, english *models.TranscriptionProfile) {
	t.Helper()
	createLanguageProfile(t, db, "fallback-es", "Spanish", "es", false, true)
	spanish = createLanguageProfile(t, db, "gpu-es", "4090 Spanish", "es", true, false)
	english = createLanguageProfile(t, db, "gpu-en", "4090 English", "en", false, false)
	createLanguageProfile(t, db, "fallback-en", "English", "en", false, true)
	return spanish, english
}

func stringPointer(value string) *string {
	return &value
}

func performYouTubeJSON(t *testing.T, path string, body map[string]any, handle func(*gin.Context)) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
	ginContext.Request.Header.Set("Content-Type", "application/json")
	handle(ginContext)
	return recorder
}

func performYouTubeRequest(t *testing.T, handler *Handler, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	return performYouTubeJSON(t, "/api/v1/transcription/youtube", body, handler.DownloadFromYouTube)
}

func performPreviewRequest(t *testing.T, handler *Handler, rawURL string) *httptest.ResponseRecorder {
	t.Helper()
	return performYouTubeJSON(t, "/api/v1/transcription/youtube/preview", map[string]any{"url": rawURL}, handler.PreviewYouTube)
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

type youTubePreviewBody struct {
	VideoID            string  `json:"video_id"`
	Title              string  `json:"title"`
	Channel            *string `json:"channel"`
	DurationSeconds    *int    `json:"duration_seconds"`
	Language           *string `json:"language"`
	ThumbnailURL       string  `json:"thumbnail_url"`
	SuggestedProfileID *string `json:"suggested_profile_id"`
}

func decodePreview(t *testing.T, recorder *httptest.ResponseRecorder) youTubePreviewBody {
	t.Helper()
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var body youTubePreviewBody
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	return body
}

func countJobs(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(&models.TranscriptionJob{}).Count(&count).Error)
	return count
}

func requireNoUploads(t *testing.T, uploadDir string) {
	t.Helper()
	leftovers, err := os.ReadDir(uploadDir)
	require.NoError(t, err)
	require.Empty(t, leftovers)
}

// Preview

func TestYouTubePreviewReturnsMetadataAndSuggestsTheEnglishProfile(t *testing.T) {
	callLog := installFakeYtDlp(t, "ok")
	handler, db, uploadDir := newYouTubeTestHandler(t)
	_, english := createHouseProfiles(t, db)

	preview := decodePreview(t, performPreviewRequest(t, handler, "https://www.youtube.com/watch?v=abc123def45"))
	require.Equal(t, "abc123def45", preview.VideoID)
	require.Equal(t, "Fake Video Title", preview.Title)
	require.Equal(t, "Fake Channel", *preview.Channel)
	require.Equal(t, 1427, *preview.DurationSeconds)
	require.Equal(t, "en-US", *preview.Language, "the language is reported as YouTube gives it")
	require.Equal(t, "https://i.ytimg.com/vi/abc123def45/maxresdefault.jpg", preview.ThumbnailURL, "the preview loads the image straight from YouTube")
	require.NotNil(t, preview.SuggestedProfileID)
	require.Equal(t, english.ID, *preview.SuggestedProfileID, "a region-tagged language matches on its base language and skips fallback profiles")

	require.Zero(t, countJobs(t, db))
	requireNoUploads(t, uploadDir)
	calls, err := os.ReadFile(callLog)
	require.NoError(t, err)
	require.NotContains(t, string(calls), "--extract-audio", "a preview never downloads")
}

func TestYouTubePreviewSuggestsTheSpanishProfile(t *testing.T) {
	installFakeYtDlp(t, "es")
	handler, db, _ := newYouTubeTestHandler(t)
	spanish, _ := createHouseProfiles(t, db)

	preview := decodePreview(t, performPreviewRequest(t, handler, "https://youtu.be/abc123def45"))
	require.NotNil(t, preview.SuggestedProfileID)
	require.Equal(t, spanish.ID, *preview.SuggestedProfileID)
}

func TestYouTubePreviewSuggestsNothingWithoutALanguage(t *testing.T) {
	installFakeYtDlp(t, "nolang")
	handler, db, _ := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)

	preview := decodePreview(t, performPreviewRequest(t, handler, "https://youtu.be/abc123def45"))
	require.Nil(t, preview.Language)
	require.Nil(t, preview.Channel)
	require.Nil(t, preview.SuggestedProfileID, "no language means the owner has to choose; the default profile is not suggested")
}

func TestYouTubePreviewSuggestsNothingForALanguageWithoutAProfile(t *testing.T) {
	installFakeYtDlp(t, "fr")
	handler, db, _ := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)

	preview := decodePreview(t, performPreviewRequest(t, handler, "https://youtu.be/abc123def45"))
	require.Equal(t, "fr", *preview.Language)
	require.Nil(t, preview.SuggestedProfileID)
}

func TestYouTubePreviewSuggestsAFallbackProfileWhenItIsTheOnlyMatch(t *testing.T) {
	installFakeYtDlp(t, "ok")
	handler, db, _ := newYouTubeTestHandler(t)
	fallbackEnglish := createLanguageProfile(t, db, "fallback-en", "English", "en", false, true)

	preview := decodePreview(t, performPreviewRequest(t, handler, "https://youtu.be/abc123def45"))
	require.NotNil(t, preview.SuggestedProfileID)
	require.Equal(t, fallbackEnglish.ID, *preview.SuggestedProfileID)
}

func TestYouTubePreviewRejectsNonYouTubeURL(t *testing.T) {
	installFakeYtDlp(t, "ok")
	handler, _, _ := newYouTubeTestHandler(t)

	response := performPreviewRequest(t, handler, "https://example.com/?redirect=youtube.com")
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Equal(t, "invalid_url", decodeYouTubeError(t, response).Code)
}

func TestYouTubePreviewReportsWhyTheVideoCannotBeUsed(t *testing.T) {
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
			handler, _, _ := newYouTubeTestHandler(t)

			response := performPreviewRequest(t, handler, "https://www.youtube.com/watch?v=abc123def45")
			require.Equal(t, testCase.status, response.Code, response.Body.String())
			require.Equal(t, testCase.code, decodeYouTubeError(t, response).Code)
		})
	}
}

// Saved thumbnail

// serveThumbnailUpstream points the handler's thumbnail fetch at a local
// server answering with the given status, and returns the path it was asked
// for.
func serveThumbnailUpstream(t *testing.T, handler *Handler, status int) *string {
	t.Helper()
	requestedPath := new(string)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		*requestedPath = request.URL.Path
		if status != http.StatusOK {
			writer.WriteHeader(status)
			return
		}
		writer.Header().Set("Content-Type", "image/jpeg")
		_, _ = writer.Write([]byte("jpeg-bytes"))
	}))
	t.Cleanup(upstream.Close)
	handler.youTubeThumbnailBaseURL = upstream.URL
	return requestedPath
}

func TestYouTubeDownloadSavesASmallThumbnailWithTheJob(t *testing.T) {
	installFakeYtDlp(t, "ok")
	handler, db, uploadDir := newYouTubeTestHandler(t)
	requestedPath := serveThumbnailUpstream(t, handler, http.StatusOK)
	_, english := createHouseProfiles(t, db)

	response := performYouTubeRequest(t, handler, map[string]any{
		"url":        "https://www.youtube.com/watch?v=abc123def45",
		"profile_id": english.ID,
	})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "/vi/abc123def45/mqdefault.jpg", *requestedPath, "the small copy comes from YouTube's image host by video id")

	var stored models.TranscriptionJob
	require.NoError(t, db.First(&stored).Error)
	require.Equal(t, models.StatusPending, stored.Status)
	require.NotNil(t, stored.ThumbnailPath)
	require.Equal(t, filepath.Join(uploadDir, "thumbnails"), filepath.Dir(*stored.ThumbnailPath), "thumbnails live apart from the audio files")
	saved, err := os.ReadFile(*stored.ThumbnailPath)
	require.NoError(t, err)
	require.Equal(t, "jpeg-bytes", string(saved))
}

func TestYouTubeDownloadWithoutAThumbnailStillQueues(t *testing.T) {
	installFakeYtDlp(t, "ok")
	handler, db, uploadDir := newYouTubeTestHandler(t)
	serveThumbnailUpstream(t, handler, http.StatusNotFound)
	_, english := createHouseProfiles(t, db)

	response := performYouTubeRequest(t, handler, map[string]any{
		"url":        "https://www.youtube.com/watch?v=abc123def45",
		"profile_id": english.ID,
	})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var stored models.TranscriptionJob
	require.NoError(t, db.First(&stored).Error)
	require.Equal(t, models.StatusPending, stored.Status)
	require.Nil(t, stored.ThumbnailPath)
	thumbnails, _ := os.ReadDir(filepath.Join(uploadDir, "thumbnails"))
	require.Empty(t, thumbnails)
}

func performThumbnailRequest(handler *Handler, jobID string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodGet, "/api/v1/transcription/"+jobID+"/thumbnail", nil)
	ginContext.Params = gin.Params{{Key: "id", Value: jobID}}
	handler.GetJobThumbnail(ginContext)
	return recorder
}

func TestJobThumbnailIsServedByScriberr(t *testing.T) {
	handler, db, uploadDir := newYouTubeTestHandler(t)
	thumbnailDir := filepath.Join(uploadDir, "thumbnails")
	require.NoError(t, os.MkdirAll(thumbnailDir, 0o755))
	thumbnailPath := filepath.Join(thumbnailDir, "job-with-thumb.jpg")
	require.NoError(t, os.WriteFile(thumbnailPath, []byte("jpeg-bytes"), 0o644))
	require.NoError(t, db.Create(&models.TranscriptionJob{ID: "job-with-thumb", AudioPath: "/x.mp3", Status: models.StatusCompleted, ThumbnailPath: &thumbnailPath}).Error)
	require.NoError(t, db.Create(&models.TranscriptionJob{ID: "job-without-thumb", AudioPath: "/y.mp3", Status: models.StatusCompleted}).Error)

	served := performThumbnailRequest(handler, "job-with-thumb")
	require.Equal(t, http.StatusOK, served.Code)
	require.Equal(t, "image/jpeg", served.Header().Get("Content-Type"))
	require.Equal(t, "jpeg-bytes", served.Body.String())

	require.Equal(t, http.StatusNotFound, performThumbnailRequest(handler, "job-without-thumb").Code)
	require.Equal(t, http.StatusNotFound, performThumbnailRequest(handler, "no-such-job").Code)
}

// Download and queue

func TestYouTubeDownloadQueuesJobWithTheChosenProfile(t *testing.T) {
	installFakeYtDlp(t, "ok")
	handler, db, uploadDir := newYouTubeTestHandler(t)
	_, english := createHouseProfiles(t, db)

	response := performYouTubeRequest(t, handler, map[string]any{
		"url":        "https://www.youtube.com/watch?v=abc123def45",
		"profile_id": english.ID,
	})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var returned models.TranscriptionJob
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &returned))
	require.Equal(t, models.StatusPending, returned.Status)

	var stored models.TranscriptionJob
	require.NoError(t, db.First(&stored, "id = ?", returned.ID).Error)
	require.Equal(t, models.StatusPending, stored.Status)
	require.Equal(t, "youtube", stored.Source)
	require.Equal(t, "Fake Video Title", *stored.Title)
	require.NotNil(t, stored.Parameters.Language)
	require.Equal(t, "en", *stored.Parameters.Language, "the chosen profile wins over the default Spanish one")
	require.NotNil(t, stored.ProfileName)
	require.Equal(t, "4090 English", *stored.ProfileName)
	require.True(t, stored.Diarization)
	require.True(t, strings.HasPrefix(stored.AudioPath, uploadDir), stored.AudioPath)
	require.FileExists(t, stored.AudioPath)
	require.Equal(t, "https://www.youtube.com/watch?v=abc123def45", *stored.SourceURL)
	require.Equal(t, "Fake Channel", *stored.SourceChannel)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.Equal(t, "https://www.youtube.com/watch?v=abc123def45", payload["source_url"])
	require.Equal(t, "Fake Channel", payload["source_channel"])
	require.Equal(t, "4090 English", payload["profile_name"])
}

func TestYouTubeDownloadRequiresAProfile(t *testing.T) {
	callLog := installFakeYtDlp(t, "ok")
	handler, db, uploadDir := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)

	response := performYouTubeRequest(t, handler, map[string]any{"url": "https://www.youtube.com/watch?v=abc123def45"})
	require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	require.Equal(t, "profile_required", decodeYouTubeError(t, response).Code)
	require.Zero(t, countJobs(t, db))
	requireNoUploads(t, uploadDir)
	_, err := os.Stat(callLog)
	require.True(t, os.IsNotExist(err), "nothing is fetched from YouTube before the request is valid")
}

func TestYouTubeDownloadRejectsAnUnknownProfile(t *testing.T) {
	installFakeYtDlp(t, "ok")
	handler, db, uploadDir := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)

	response := performYouTubeRequest(t, handler, map[string]any{
		"url":        "https://www.youtube.com/watch?v=abc123def45",
		"profile_id": "no-such-profile",
	})
	require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	require.Equal(t, "unknown_profile", decodeYouTubeError(t, response).Code)
	require.Zero(t, countJobs(t, db))
	requireNoUploads(t, uploadDir)
}

func TestYouTubeDownloadStoresTrimmedURLAndSkipsMissingChannel(t *testing.T) {
	installFakeYtDlp(t, "nolang")
	handler, db, _ := newYouTubeTestHandler(t)
	spanish, _ := createHouseProfiles(t, db)

	response := performYouTubeRequest(t, handler, map[string]any{
		"url":        "  https://youtu.be/abc123def45  ",
		"profile_id": spanish.ID,
	})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var stored models.TranscriptionJob
	require.NoError(t, db.First(&stored).Error)
	require.Equal(t, "https://youtu.be/abc123def45", *stored.SourceURL)
	require.Nil(t, stored.SourceChannel)
	require.Equal(t, "Fake Video Title", *stored.Title)
}

func TestYouTubeDownloadSurvivesClientDisconnect(t *testing.T) {
	installFakeYtDlp(t, "ok")
	handler, db, _ := newYouTubeTestHandler(t)
	_, english := createHouseProfiles(t, db)

	encoded, err := json.Marshal(map[string]any{"url": "https://www.youtube.com/watch?v=abc123def45", "profile_id": english.ID})
	require.NoError(t, err)
	// A browser that is already gone: once the request has been read, nothing
	// the handler does may depend on the client still waiting.
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
	_, english := createHouseProfiles(t, db)

	response := performYouTubeRequest(t, handler, map[string]any{
		"url":        "https://youtu.be/abc123def45",
		"title":      "My own title",
		"profile_id": english.ID,
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
	_, english := createHouseProfiles(t, db)
	handler.taskQueue.Stop()

	response := performYouTubeRequest(t, handler, map[string]any{
		"url":        "https://www.youtube.com/watch?v=abc123def45",
		"profile_id": english.ID,
	})
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

func TestYouTubeDownloadRejectsNonYouTubeURL(t *testing.T) {
	installFakeYtDlp(t, "ok")
	handler, db, _ := newYouTubeTestHandler(t)
	_, english := createHouseProfiles(t, db)

	for _, rawURL := range []string{
		"https://example.com/watch?v=abc123def45",
		"https://notyoutube.com.example.org/watch?v=abc123def45",
		"https://example.com/?redirect=youtube.com",
		"not a url",
	} {
		response := performYouTubeRequest(t, handler, map[string]any{"url": rawURL, "profile_id": english.ID})
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
			_, english := createHouseProfiles(t, db)

			response := performYouTubeRequest(t, handler, map[string]any{
				"url":        "https://www.youtube.com/watch?v=abc123def45",
				"profile_id": english.ID,
			})
			require.Equal(t, testCase.status, response.Code, response.Body.String())
			require.Equal(t, testCase.code, decodeYouTubeError(t, response).Code)
			require.Zero(t, countJobs(t, db))
			requireNoUploads(t, uploadDir)
		})
	}
}
