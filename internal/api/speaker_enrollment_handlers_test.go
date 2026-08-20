package api

import (
	"bytes"
	"context"
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

type fakeSpeakerEmbeddingExtractor struct {
	result       *SpeakerEmbeddingResult
	err          error
	gotAudioPath string
	gotSegments  []SpeakerSegment
}

func (f *fakeSpeakerEmbeddingExtractor) ExtractSpeakerEmbedding(ctx context.Context, audioPath string, segments []SpeakerSegment) (*SpeakerEmbeddingResult, error) {
	f.gotAudioPath = audioPath
	f.gotSegments = segments
	return f.result, f.err
}

func newEnrollmentTestHandler(t *testing.T, extractor *fakeSpeakerEmbeddingExtractor) (*Handler, *gorm.DB) {
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
	handler := &Handler{
		jobRepo:                   repository.NewJobRepository(db),
		speakerProfileRepo:        repository.NewSpeakerProfileRepository(db),
		speakerEmbeddingExtractor: extractor,
	}
	return handler, db
}

func seedEnrollmentFixtures(t *testing.T, db *gorm.DB) (models.SpeakerProfile, models.TranscriptionJob) {
	t.Helper()
	profile := models.SpeakerProfile{Name: "Ada"}
	require.NoError(t, db.Create(&profile).Error)
	transcript := `{"segments":[` +
		`{"start":1.0,"end":9.5,"text":"first","speaker":"SPEAKER_00"},` +
		`{"start":10.0,"end":14.0,"text":"second","speaker":"SPEAKER_01"},` +
		`{"start":15.0,"end":19.0,"text":"third","speaker":"SPEAKER_00"}]}`
	job := models.TranscriptionJob{ID: "enrollment-job-id", AudioPath: "/srv/audio/enrollment.wav", Transcript: &transcript, Diarization: true}
	require.NoError(t, db.Create(&job).Error)
	return profile, job
}

func performEnrollmentRequest(t *testing.T, handler *Handler, profileID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	requestContext, _ := gin.CreateTestContext(recorder)
	requestContext.Request = httptest.NewRequest(http.MethodPost, "/speakers/profiles/"+profileID+"/samples", bytes.NewReader(encoded))
	requestContext.Request.Header.Set("Content-Type", "application/json")
	requestContext.Params = gin.Params{{Key: "id", Value: profileID}}
	handler.EnrollSpeakerProfileSample(requestContext)
	return recorder
}

func TestEnrollSpeakerSampleCreatesRowFromDiarizedJob(t *testing.T) {
	extractor := &fakeSpeakerEmbeddingExtractor{result: &SpeakerEmbeddingResult{
		Dimensions:   256,
		Embedding:    []byte{0x00, 0x00, 0x80, 0x3f, 0x00, 0x00, 0x00, 0x40},
		SecondsUsed:  12.5,
		SegmentsUsed: 2,
	}}
	handler, db := newEnrollmentTestHandler(t, extractor)
	profile, job := seedEnrollmentFixtures(t, db)

	response := performEnrollmentRequest(t, handler, fmt.Sprint(profile.ID), map[string]any{"job_id": job.ID, "speaker_label": "SPEAKER_00"})
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())

	require.Equal(t, job.AudioPath, extractor.gotAudioPath)
	require.Len(t, extractor.gotSegments, 2, "only the requested speaker's segments reach the extractor")
	for _, segment := range extractor.gotSegments {
		require.Equal(t, "SPEAKER_00", segment.Speaker)
	}

	var samples []models.SpeakerProfileSample
	require.NoError(t, db.Find(&samples).Error)
	require.Len(t, samples, 1)
	require.Equal(t, profile.ID, samples[0].SpeakerProfileID)
	require.Equal(t, "enrollment", samples[0].Source)
	require.Equal(t, job.ID, samples[0].SourceJobID)
	require.Equal(t, "SPEAKER_00", samples[0].SourceSpeakerLabel)
	require.Equal(t, 256, samples[0].Dimensions)
	require.InDelta(t, 12.5, samples[0].SecondsUsed, 0.0001)
	require.NotEmpty(t, samples[0].Embedding)

	var decoded any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &decoded))
	assertNoForbiddenKeyDeep(t, decoded, "embedding", "embeddings", "Embedding")
}

func TestEnrollSpeakerSampleUnknownJobReturns404(t *testing.T) {
	extractor := &fakeSpeakerEmbeddingExtractor{}
	handler, db := newEnrollmentTestHandler(t, extractor)
	profile, _ := seedEnrollmentFixtures(t, db)

	response := performEnrollmentRequest(t, handler, fmt.Sprint(profile.ID), map[string]any{"job_id": "no-such-job", "speaker_label": "SPEAKER_00"})
	require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
}

func TestEnrollSpeakerSampleUnknownProfileReturns404(t *testing.T) {
	extractor := &fakeSpeakerEmbeddingExtractor{}
	handler, db := newEnrollmentTestHandler(t, extractor)
	_, job := seedEnrollmentFixtures(t, db)

	response := performEnrollmentRequest(t, handler, "999999", map[string]any{"job_id": job.ID, "speaker_label": "SPEAKER_00"})
	require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
}

func TestEnrollSpeakerSampleAbsentLabelReturns400(t *testing.T) {
	extractor := &fakeSpeakerEmbeddingExtractor{}
	handler, db := newEnrollmentTestHandler(t, extractor)
	profile, job := seedEnrollmentFixtures(t, db)

	response := performEnrollmentRequest(t, handler, fmt.Sprint(profile.ID), map[string]any{"job_id": job.ID, "speaker_label": "SPEAKER_07"})
	require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "SPEAKER_07", "error message names the missing label so the caller can act on it")

	var count int64
	require.NoError(t, db.Model(&models.SpeakerProfileSample{}).Count(&count).Error)
	require.EqualValues(t, 0, count)
}

func TestEnrollSpeakerSampleUnderAudioFloorReturns422AndNoRow(t *testing.T) {
	extractor := &fakeSpeakerEmbeddingExtractor{result: &SpeakerEmbeddingResult{
		Dimensions:   256,
		Embedding:    nil,
		SecondsUsed:  1.2,
		SegmentsUsed: 1,
	}}
	handler, db := newEnrollmentTestHandler(t, extractor)
	profile, job := seedEnrollmentFixtures(t, db)

	response := performEnrollmentRequest(t, handler, fmt.Sprint(profile.ID), map[string]any{"job_id": job.ID, "speaker_label": "SPEAKER_00"})
	require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())

	var count int64
	require.NoError(t, db.Model(&models.SpeakerProfileSample{}).Count(&count).Error)
	require.EqualValues(t, 0, count)
}

func TestEnrollSpeakerSampleRepeatEnrollmentAddsSecondSample(t *testing.T) {
	extractor := &fakeSpeakerEmbeddingExtractor{result: &SpeakerEmbeddingResult{
		Dimensions:   256,
		Embedding:    []byte{0x00, 0x00, 0x80, 0x3f},
		SecondsUsed:  12.5,
		SegmentsUsed: 2,
	}}
	handler, db := newEnrollmentTestHandler(t, extractor)
	profile, job := seedEnrollmentFixtures(t, db)

	first := performEnrollmentRequest(t, handler, fmt.Sprint(profile.ID), map[string]any{"job_id": job.ID, "speaker_label": "SPEAKER_00"})
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
	second := performEnrollmentRequest(t, handler, fmt.Sprint(profile.ID), map[string]any{"job_id": job.ID, "speaker_label": "SPEAKER_00"})
	require.Equal(t, http.StatusCreated, second.Code, second.Body.String())

	var count int64
	require.NoError(t, db.Model(&models.SpeakerProfileSample{}).Count(&count).Error)
	require.EqualValues(t, 2, count, "multiple samples per profile is the intended shape")
}
