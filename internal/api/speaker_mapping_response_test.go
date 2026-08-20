package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"scriberr/internal/models"
	"scriberr/internal/repository"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newSpeakerMappingTestHandler(t *testing.T) (*Handler, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&models.TranscriptionJob{},
		&models.MultiTrackFile{},
		&models.SpeakerMapping{},
		&models.SpeakerProfile{},
		&models.SpeakerProfileSample{},
	))
	return &Handler{
		jobRepo:            repository.NewJobRepository(db),
		speakerMappingRepo: repository.NewSpeakerMappingRepository(db),
	}, db
}

func getSpeakerMappingsPayload(t *testing.T, handler *Handler, jobID string) []map[string]any {
	t.Helper()
	recorder := httptest.NewRecorder()
	requestContext, _ := gin.CreateTestContext(recorder)
	requestContext.Request = httptest.NewRequest(http.MethodGet, "/transcription/"+jobID+"/speakers", nil)
	requestContext.Params = gin.Params{{Key: "id", Value: jobID}}
	handler.GetSpeakerMappings(requestContext)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var payload []map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	return payload
}

func TestGetSpeakerMappingsCarriesAutoProvenance(t *testing.T) {
	handler, db := newSpeakerMappingTestHandler(t)
	job := models.TranscriptionJob{ID: "provenance-job", AudioPath: "/srv/audio/provenance.wav", Diarization: true}
	require.NoError(t, db.Create(&job).Error)
	profileID := uint(7)
	confidence := 0.87
	require.NoError(t, db.Create(&models.SpeakerMapping{
		TranscriptionJobID: job.ID,
		OriginalSpeaker:    "SPEAKER_00",
		CustomName:         "Ada",
		Source:             "auto",
		Confidence:         &confidence,
		SpeakerProfileID:   &profileID,
	}).Error)

	payload := getSpeakerMappingsPayload(t, handler, job.ID)
	require.Len(t, payload, 1)
	require.Equal(t, "auto", payload[0]["source"])
	require.InDelta(t, 0.87, payload[0]["confidence"], 0.0001)
	require.EqualValues(t, 7, payload[0]["speaker_profile_id"])
}

func TestGetSpeakerMappingsLegacyRowsReadAsManual(t *testing.T) {
	handler, db := newSpeakerMappingTestHandler(t)
	job := models.TranscriptionJob{ID: "legacy-job", AudioPath: "/srv/audio/legacy.wav", Diarization: true}
	require.NoError(t, db.Create(&job).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO speaker_mappings (transcription_job_id, original_speaker, custom_name, source) VALUES (?, ?, ?, '')",
		job.ID, "SPEAKER_01", "Grace",
	).Error)

	payload := getSpeakerMappingsPayload(t, handler, job.ID)
	require.Len(t, payload, 1)
	require.Equal(t, "manual", payload[0]["source"])
	_, hasConfidence := payload[0]["confidence"]
	require.False(t, hasConfidence)
}
