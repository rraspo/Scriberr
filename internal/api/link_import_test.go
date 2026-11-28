package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scriberr/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// fakeSocialYtDlpScript stands in for yt-dlp on Instagram and TikTok links.
// FAKE_YTDLP_MODE picks the post: "ig" is an Instagram reel with a caption
// and no language, "tt" a TikTok video in English, "ig_noaudio" a reel whose
// download has no audio track, "ig_login" and "ig_429" Instagram refusing.
const fakeSocialYtDlpScript = `#!/bin/sh
mode="${FAKE_YTDLP_MODE:-ig}"
if [ -n "$FAKE_YTDLP_LOG" ]; then echo "$*" >> "$FAKE_YTDLP_LOG"; fi
case "$mode" in
  ig_login)
    echo "ERROR: [Instagram] ABC123xyz: Requested content is not available, rate-limit reached or login required. Use --cookies, --cookies-from-browser, --username and --password, --netrc-cmd, or --netrc (instagram) to provide account credentials" >&2
    exit 1 ;;
  ig_429)
    echo "ERROR: [Instagram] ABC123xyz: Unable to download webpage: HTTP Error 429: Too Many Requests (caused by <HTTPError 429: Too Many Requests>)" >&2
    exit 1 ;;
esac
output=""
previous=""
for argument in "$@"; do
  if [ "$argument" = "--dump-json" ] || [ "$argument" = "-j" ]; then
    case "$mode" in
      tt_es) echo '{"id":"7000000000000000001","title":"Tres prompts","description":"Tres prompts","channel":"Creator Display","uploader":"creator","duration":30,"language":"es"}' ;;
      tt) echo '{"id":"7000000000000000001","title":"Three prompts that save me an hour","description":"Three prompts that save me an hour #ai","channel":"Creator Display","uploader":"creator","duration":30,"language":"en"}' ;;
      *) printf '%s\n' '{"id":"ABC123xyz","title":"Video by creator.handle","description":"Three AI tools I use every day\n\n1. A note taker\n#ai #tools","channel":"creator.handle","uploader":"Creator Name","duration":null,"language":null,"thumbnail":"https://cdn.example.com/thumb.jpg"}' ;;
    esac
    exit 0
  fi
  if [ "$previous" = "--output" ] || [ "$previous" = "-o" ]; then
    output="$argument"
  fi
  previous="$argument"
done
if [ "$mode" = "ig_noaudio" ]; then
  target=$(echo "$output" | sed 's/%(ext)s/mp4/')
  printf 'videoonly' > "$target"
  echo "ERROR: Postprocessing: WARNING: unable to obtain file audio codec with ffprobe" >&2
  exit 1
fi
if [ -n "$output" ]; then
  target=$(echo "$output" | sed 's/%(ext)s/mp3/')
  printf 'ID3fake' > "$target"
fi
exit 0
`

func installFakeSocialYtDlp(t *testing.T, mode string) string {
	t.Helper()
	binDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "yt-dlp"), []byte(fakeSocialYtDlpScript), 0o755))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_YTDLP_MODE", mode)
	logPath := filepath.Join(binDir, "calls.log")
	t.Setenv("FAKE_YTDLP_LOG", logPath)
	return logPath
}

func countCalls(t *testing.T, logPath string) int {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	return strings.Count(string(data), "\n")
}

func performLinkImport(t *testing.T, handler *Handler, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	return performYouTubeJSON(t, "/api/v1/transcription/links", body, handler.ImportLink)
}

func performLinkStatus(handler *Handler, jobID string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodGet, "/api/v1/transcription/links/"+jobID, nil)
	ginContext.Params = gin.Params{{Key: "id", Value: jobID}}
	handler.GetLinkImport(ginContext)
	return recorder
}

type linkImportBody struct {
	JobID         string  `json:"job_id"`
	Status        string  `json:"status"`
	Title         string  `json:"title"`
	Source        string  `json:"source"`
	ProfileName   string  `json:"profile_name"`
	RecordingPath string  `json:"recording_path"`
	Error         *string `json:"error"`
}

func decodeLinkImport(t *testing.T, recorder *httptest.ResponseRecorder) linkImportBody {
	t.Helper()
	var body linkImportBody
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body), recorder.Body.String())
	return body
}

func TestLinkImportQueuesAnInstagramReelWithItsCaption(t *testing.T) {
	installFakeSocialYtDlp(t, "ig")
	handler, db, uploadDir := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)

	response := performLinkImport(t, handler, map[string]any{
		"url":         " https://www.instagram.com/reel/ABC123xyz/ ",
		"external_id": "wamid.example:0",
	})
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	body := decodeLinkImport(t, response)
	require.NotEmpty(t, body.JobID)
	require.Equal(t, "pending", body.Status)
	require.Equal(t, "instagram", body.Source)
	require.Equal(t, "4090 English", body.ProfileName, "no language from Instagram means English")
	require.Equal(t, "/audio/"+body.JobID, body.RecordingPath)
	// Instagram's own title is "Video by <handle>"; the caption's first line says more.
	require.Equal(t, "Three AI tools I use every day", body.Title)

	var stored models.TranscriptionJob
	require.NoError(t, db.First(&stored, "id = ?", body.JobID).Error)
	require.Equal(t, models.StatusPending, stored.Status)
	require.Equal(t, "instagram", stored.Source)
	require.Equal(t, "https://www.instagram.com/reel/ABC123xyz/", *stored.SourceURL)
	require.Equal(t, "@creator.handle", *stored.SourceChannel)
	require.Equal(t, "Three AI tools I use every day\n\n1. A note taker\n#ai #tools", *stored.SourceDescription)
	require.Equal(t, "wamid.example:0", *stored.ExternalID)
	require.Equal(t, "en", *stored.Parameters.Language)
	require.True(t, strings.HasPrefix(stored.AudioPath, uploadDir))
	require.FileExists(t, stored.AudioPath)
	require.Nil(t, stored.ThumbnailPath, "only YouTube thumbnails are fetched")
}

func TestLinkImportQueuesATikTokVideoInItsLanguage(t *testing.T) {
	installFakeSocialYtDlp(t, "tt")
	handler, db, _ := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)

	response := performLinkImport(t, handler, map[string]any{"url": "https://www.tiktok.com/@creator/video/7000000000000000001"})
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	body := decodeLinkImport(t, response)
	require.Equal(t, "tiktok", body.Source)
	require.Equal(t, "4090 English", body.ProfileName)
	require.Equal(t, "Three prompts that save me an hour", body.Title)

	var stored models.TranscriptionJob
	require.NoError(t, db.First(&stored, "id = ?", body.JobID).Error)
	require.Equal(t, "tiktok", stored.Source)
	require.Equal(t, "@creator", *stored.SourceChannel, "TikTok's handle is the uploader, not the display name")
	require.Equal(t, "Three prompts that save me an hour #ai", *stored.SourceDescription)
	require.Nil(t, stored.ExternalID)
}

func TestLinkImportReturnsTheExistingJobForARepeatedExternalID(t *testing.T) {
	callLog := installFakeSocialYtDlp(t, "ig")
	handler, db, _ := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)
	request := map[string]any{"url": "https://www.instagram.com/p/ABC123xyz/", "external_id": "wamid.example:1"}

	first := performLinkImport(t, handler, request)
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
	callsAfterFirst := countCalls(t, callLog)

	second := performLinkImport(t, handler, request)
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())
	require.Equal(t, decodeLinkImport(t, first).JobID, decodeLinkImport(t, second).JobID)
	require.Equal(t, int64(1), countJobs(t, db))
	require.Equal(t, callsAfterFirst, countCalls(t, callLog), "a retry never downloads again")
}

func TestLinkImportRefusesLinksItCannotTake(t *testing.T) {
	callLog := installFakeSocialYtDlp(t, "ig")
	handler, db, _ := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)

	for _, rawURL := range []string{
		"https://x.com/someone/status/1800000000000000000",
		"https://twitter.com/someone/status/1800000000000000000",
		"https://www.instagram.com/creator.handle/",
		"https://www.instagram.com/stories/creator.handle/1/",
		"https://instagram.com.example.org/reel/ABC123xyz/",
		"https://example.com/reel/ABC123xyz/",
		"ftp://www.instagram.com/reel/ABC123xyz/",
		"not a url",
	} {
		response := performLinkImport(t, handler, map[string]any{"url": rawURL})
		require.Equal(t, http.StatusBadRequest, response.Code, rawURL)
		require.Equal(t, "unsupported_url", decodeYouTubeError(t, response).Code, rawURL)
	}
	missing := performLinkImport(t, handler, map[string]any{"external_id": "wamid.example:2"})
	require.Equal(t, http.StatusBadRequest, missing.Code)
	require.Equal(t, "invalid_request", decodeYouTubeError(t, missing).Code)
	require.Zero(t, countJobs(t, db))
	require.Zero(t, countCalls(t, callLog))
}

func TestLinkImportAcceptsEverySupportedLinkShape(t *testing.T) {
	for _, testCase := range []struct {
		url    string
		source string
	}{
		{"https://www.instagram.com/reel/ABC123xyz/", "instagram"},
		{"https://instagram.com/reels/ABC123xyz", "instagram"},
		{"https://www.instagram.com/p/ABC123xyz/", "instagram"},
		{"https://www.tiktok.com/@creator/video/7000000000000000001", "tiktok"},
		{"https://m.tiktok.com/@creator/video/7000000000000000001", "tiktok"},
		{"https://vm.tiktok.com/ZMabc123/", "tiktok"},
		{"https://vt.tiktok.com/ZSabc123/", "tiktok"},
		{"https://www.youtube.com/watch?v=abc123def45", "youtube"},
		{"https://youtu.be/abc123def45", "youtube"},
		{"https://www.youtube.com/shorts/abc123def45", "youtube"},
	} {
		source, ok := linkSource(testCase.url)
		require.True(t, ok, testCase.url)
		require.Equal(t, testCase.source, source, testCase.url)
	}
}

func TestLinkImportExplainsWhyInstagramRefused(t *testing.T) {
	for _, testCase := range []struct {
		mode   string
		status int
		code   string
	}{
		{mode: "ig_login", status: http.StatusBadGateway, code: "instagram_login_required"},
		{mode: "ig_429", status: http.StatusBadGateway, code: "rate_limited"},
	} {
		t.Run(testCase.mode, func(t *testing.T) {
			installFakeSocialYtDlp(t, testCase.mode)
			handler, db, uploadDir := newYouTubeTestHandler(t)
			createHouseProfiles(t, db)

			response := performLinkImport(t, handler, map[string]any{"url": "https://www.instagram.com/reel/ABC123xyz/"})
			require.Equal(t, testCase.status, response.Code, response.Body.String())
			require.Equal(t, testCase.code, decodeYouTubeError(t, response).Code)
			require.Zero(t, countJobs(t, db))
			requireNoUploads(t, uploadDir)
		})
	}
}

// A reel with no audio track still carries its caption, which is often the
// whole point of the post, so the job is kept, marked failed with the reason.
func TestLinkImportKeepsTheCaptionOfAReelWithoutAudio(t *testing.T) {
	installFakeSocialYtDlp(t, "ig_noaudio")
	handler, db, _ := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)

	response := performLinkImport(t, handler, map[string]any{"url": "https://www.instagram.com/reel/ABC123xyz/", "external_id": "wamid.example:3"})
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	body := decodeLinkImport(t, response)
	require.Equal(t, "failed", body.Status)
	require.NotNil(t, body.Error)
	require.Contains(t, *body.Error, "no audio")

	var stored models.TranscriptionJob
	require.NoError(t, db.First(&stored, "id = ?", body.JobID).Error)
	require.Equal(t, models.StatusFailed, stored.Status)
	require.Contains(t, *stored.ErrorMessage, "no audio")
	require.Equal(t, "Three AI tools I use every day\n\n1. A note taker\n#ai #tools", *stored.SourceDescription)
}

func TestLinkImportFallsBackToSpanishWhenThereIsNoEnglishProfile(t *testing.T) {
	installFakeSocialYtDlp(t, "ig")
	handler, db, _ := newYouTubeTestHandler(t)
	createLanguageProfile(t, db, "gpu-es", "4090 Spanish", "es", true, false)

	response := performLinkImport(t, handler, map[string]any{"url": "https://www.instagram.com/reel/ABC123xyz/"})
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	require.Equal(t, "4090 Spanish", decodeLinkImport(t, response).ProfileName)
}

// A reported language still wins over the English default.
func TestLinkImportUsesTheReportedLanguageBeforeEnglish(t *testing.T) {
	installFakeSocialYtDlp(t, "tt_es")
	handler, db, _ := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)

	response := performLinkImport(t, handler, map[string]any{"url": "https://www.tiktok.com/@creator/video/7000000000000000001"})
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	require.Equal(t, "4090 Spanish", decodeLinkImport(t, response).ProfileName)
}

func TestLinkImportWithoutAnyUsableProfile(t *testing.T) {
	callLog := installFakeSocialYtDlp(t, "ig")
	handler, db, _ := newYouTubeTestHandler(t)
	createLanguageProfile(t, db, "gpu-fr", "French", "fr", true, false)

	response := performLinkImport(t, handler, map[string]any{"url": "https://www.instagram.com/reel/ABC123xyz/"})
	require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
	require.Equal(t, "no_profile", decodeYouTubeError(t, response).Code)
	require.Zero(t, countJobs(t, db))
	require.Equal(t, 1, countCalls(t, callLog), "only the metadata call ran")
}

func TestLinkImportStatusReportsTheJob(t *testing.T) {
	installFakeSocialYtDlp(t, "ig")
	handler, db, _ := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)
	created := decodeLinkImport(t, performLinkImport(t, handler, map[string]any{"url": "https://www.instagram.com/reel/ABC123xyz/"}))

	pending := performLinkStatus(handler, created.JobID)
	require.Equal(t, http.StatusOK, pending.Code, pending.Body.String())
	body := decodeLinkImport(t, pending)
	require.Equal(t, created.JobID, body.JobID)
	require.Equal(t, "pending", body.Status)
	require.Equal(t, "instagram", body.Source)
	require.Equal(t, "/audio/"+created.JobID, body.RecordingPath)
	require.Nil(t, body.Error)
	require.Contains(t, pending.Body.String(), `"error":null`)

	reason := "WhisperX remote execution failed"
	require.NoError(t, db.Model(&models.TranscriptionJob{}).Where("id = ?", created.JobID).
		Updates(map[string]any{"status": models.StatusFailed, "error_message": reason}).Error)
	failed := decodeLinkImport(t, performLinkStatus(handler, created.JobID))
	require.Equal(t, "failed", failed.Status)
	require.Equal(t, reason, *failed.Error)

	missing := performLinkStatus(handler, "00000000-0000-0000-0000-000000000000")
	require.Equal(t, http.StatusNotFound, missing.Code)
	require.Equal(t, "not_found", decodeYouTubeError(t, missing).Code)
}

// The dialog in the app uses the same import, with the profile the owner picks.
func TestDialogImportTakesAnInstagramLink(t *testing.T) {
	installFakeSocialYtDlp(t, "ig")
	handler, db, _ := newYouTubeTestHandler(t)
	_, english := createHouseProfiles(t, db)

	response := performYouTubeRequest(t, handler, map[string]any{"url": "https://www.instagram.com/reel/ABC123xyz/", "profile_id": english.ID})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var stored models.TranscriptionJob
	require.NoError(t, db.First(&stored).Error)
	require.Equal(t, "instagram", stored.Source)
	require.Equal(t, "4090 English", *stored.ProfileName)
	require.Equal(t, "@creator.handle", *stored.SourceChannel)
}

func TestDialogPreviewSuggestsEnglishForAnInstagramReel(t *testing.T) {
	installFakeSocialYtDlp(t, "ig")
	handler, db, _ := newYouTubeTestHandler(t)
	_, english := createHouseProfiles(t, db)

	preview := decodePreview(t, performPreviewRequest(t, handler, "https://www.instagram.com/reel/ABC123xyz/"))
	require.Equal(t, "Three AI tools I use every day", preview.Title)
	require.Equal(t, "@creator.handle", *preview.Channel)
	require.NotNil(t, preview.SuggestedProfileID)
	require.Equal(t, english.ID, *preview.SuggestedProfileID)
}

func TestDescribeSourceForSocialLinks(t *testing.T) {
	handle := "@creator.handle"
	instagram := describeTestJob("instagram", "Three AI tools", "/data/uploads/i.mp3")
	instagram.SourceChannel = &handle
	require.Equal(t, SourceDescription{"instagram", "@creator.handle · Instagram"}, describeSource(instagram))
	require.Equal(t, SourceDescription{"instagram", "Instagram"}, describeSource(describeTestJob("instagram", "Reel", "/data/uploads/i.mp3")))
	tiktok := describeTestJob("tiktok", "Prompts", "/data/uploads/t.mp3")
	tiktok.SourceChannel = &handle
	require.Equal(t, SourceDescription{"tiktok", "@creator.handle · TikTok"}, describeSource(tiktok))
}
