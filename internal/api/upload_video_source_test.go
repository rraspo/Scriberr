package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"scriberr/internal/models"
	"scriberr/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// fakeFfmpegScript writes a stand-in mp3 at the last argument, the output
// path of the audio extraction.
const fakeFfmpegScript = `#!/bin/sh
for argument in "$@"; do output="$argument"; done
printf 'ID3fake' > "$output"
`

func performVideoUpload(t *testing.T, handler *Handler, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	binDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "ffmpeg"), []byte(fakeFfmpegScript), 0o755))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("video", "clip.mp4")
	require.NoError(t, err)
	_, err = part.Write([]byte("fake video"))
	require.NoError(t, err)
	for name, value := range fields {
		require.NoError(t, writer.WriteField(name, value))
	}
	require.NoError(t, writer.Close())

	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/api/v1/transcription/upload-video", &body)
	ginContext.Request.Header.Set("Content-Type", writer.FormDataContentType())
	handler.UploadVideo(ginContext)
	return recorder
}

func TestUploadVideoTagsTheJobAsVideo(t *testing.T) {
	handler, db, _ := newYouTubeTestHandler(t)
	handler.fileService = service.NewFileService()

	response := performVideoUpload(t, handler, map[string]string{"title": "Screen recording"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var stored models.TranscriptionJob
	require.NoError(t, db.First(&stored).Error)
	require.Equal(t, "video", stored.Source)
	require.Equal(t, "video", describeSource(stored).Kind)
	require.Equal(t, "Video", describeSource(stored).Label)
}

func TestUploadVideoKeepsAnExplicitSource(t *testing.T) {
	handler, db, _ := newYouTubeTestHandler(t)
	handler.fileService = service.NewFileService()

	response := performVideoUpload(t, handler, map[string]string{"source": "plaud"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var stored models.TranscriptionJob
	require.NoError(t, db.First(&stored).Error)
	require.Equal(t, "plaud", stored.Source)
}
