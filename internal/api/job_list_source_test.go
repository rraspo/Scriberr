package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"scriberr/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestJobListCarriesSourceKindAndLabel(t *testing.T) {
	handler, db, _ := newYouTubeTestHandler(t)
	channel := "Some Channel"
	title := "A video"
	require.NoError(t, db.Create(&models.TranscriptionJob{
		ID: "job-yt", AudioPath: "/x.mp3", Status: models.StatusCompleted,
		Source: "youtube", SourceChannel: &channel, Title: &title,
	}).Error)

	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodGet, "/api/v1/transcription/list", nil)
	handler.ListTranscriptionJobs(ginContext)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var body struct {
		Jobs []map[string]any `json:"jobs"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Len(t, body.Jobs, 1)
	require.Equal(t, "youtube", body.Jobs[0]["source_kind"])
	require.Equal(t, "Some Channel · YouTube", body.Jobs[0]["source_label"])
}
