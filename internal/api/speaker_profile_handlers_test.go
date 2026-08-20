package api

import (
	"bytes"
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

func newSpeakerProfileTestHandler(t *testing.T) (*Handler, *gorm.DB) {
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
	return &Handler{speakerProfileRepo: repository.NewSpeakerProfileRepository(db)}, db
}

func performSpeakerProfileRequest(t *testing.T, method string, body any, params gin.Params, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		require.NoError(t, err)
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(method, "/speakers/profiles", bytes.NewReader(encoded))
	context.Request.Header.Set("Content-Type", "application/json")
	context.Params = params
	handler(context)
	return recorder
}

func seedSpeakerProfileSample(t *testing.T, db *gorm.DB, profileID uint, sourceJobID, label string) models.SpeakerProfileSample {
	t.Helper()
	sample := models.SpeakerProfileSample{
		SpeakerProfileID:   profileID,
		Embedding:          []byte{0x00, 0x00, 0x80, 0x3f, 0x00, 0x00, 0x00, 0x40},
		Dimensions:         2,
		Source:             "enrollment",
		SourceJobID:        sourceJobID,
		SourceSpeakerLabel: label,
		SecondsUsed:        12.5,
	}
	require.NoError(t, db.Create(&sample).Error)
	return sample
}

func assertNoForbiddenKeyDeep(t *testing.T, value any, forbidden ...string) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			for _, name := range forbidden {
				require.NotEqual(t, name, key, "forbidden key %q present in response", name)
			}
			assertNoForbiddenKeyDeep(t, nested, forbidden...)
		}
	case []any:
		for _, element := range typed {
			assertNoForbiddenKeyDeep(t, element, forbidden...)
		}
	}
}

func TestSpeakerProfileCreateAndList(t *testing.T) {
	handler, _ := newSpeakerProfileTestHandler(t)
	created := performSpeakerProfileRequest(t, http.MethodPost, map[string]any{"name": "Ada"}, nil, handler.CreateSpeakerProfile)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())

	list := performSpeakerProfileRequest(t, http.MethodGet, nil, nil, handler.ListSpeakerProfiles)
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	var profiles []map[string]any
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &profiles))
	require.Len(t, profiles, 1)
	require.Equal(t, "Ada", profiles[0]["name"])
	require.EqualValues(t, 0, profiles[0]["sample_count"])
}

func TestSpeakerProfileDuplicateNameConflicts(t *testing.T) {
	handler, db := newSpeakerProfileTestHandler(t)
	first := performSpeakerProfileRequest(t, http.MethodPost, map[string]any{"name": "Ada"}, nil, handler.CreateSpeakerProfile)
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
	second := performSpeakerProfileRequest(t, http.MethodPost, map[string]any{"name": "Ada"}, nil, handler.CreateSpeakerProfile)
	require.Equal(t, http.StatusConflict, second.Code, second.Body.String())

	var count int64
	require.NoError(t, db.Model(&models.SpeakerProfile{}).Where("name = ?", "Ada").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestSpeakerProfileRename(t *testing.T) {
	handler, db := newSpeakerProfileTestHandler(t)
	created := performSpeakerProfileRequest(t, http.MethodPost, map[string]any{"name": "Ada"}, nil, handler.CreateSpeakerProfile)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var profile models.SpeakerProfile
	require.NoError(t, db.Where("name = ?", "Ada").First(&profile).Error)

	params := gin.Params{{Key: "id", Value: fmt.Sprint(profile.ID)}}
	renamed := performSpeakerProfileRequest(t, http.MethodPatch, map[string]any{"name": "Ada Lovelace"}, params, handler.UpdateSpeakerProfile)
	require.Equal(t, http.StatusOK, renamed.Code, renamed.Body.String())

	list := performSpeakerProfileRequest(t, http.MethodGet, nil, nil, handler.ListSpeakerProfiles)
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	var profiles []map[string]any
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &profiles))
	require.Len(t, profiles, 1)
	require.Equal(t, "Ada Lovelace", profiles[0]["name"])
}

func TestSpeakerProfileDeleteRemovesSamples(t *testing.T) {
	handler, db := newSpeakerProfileTestHandler(t)
	created := performSpeakerProfileRequest(t, http.MethodPost, map[string]any{"name": "Ada"}, nil, handler.CreateSpeakerProfile)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var profile models.SpeakerProfile
	require.NoError(t, db.Where("name = ?", "Ada").First(&profile).Error)
	seedSpeakerProfileSample(t, db, profile.ID, "job-a", "SPEAKER_00")
	seedSpeakerProfileSample(t, db, profile.ID, "job-b", "SPEAKER_01")

	params := gin.Params{{Key: "id", Value: fmt.Sprint(profile.ID)}}
	deleted := performSpeakerProfileRequest(t, http.MethodDelete, nil, params, handler.DeleteSpeakerProfile)
	require.True(t, deleted.Code == http.StatusOK || deleted.Code == http.StatusNoContent, deleted.Body.String())

	var profileCount, sampleCount int64
	require.NoError(t, db.Model(&models.SpeakerProfile{}).Count(&profileCount).Error)
	require.NoError(t, db.Model(&models.SpeakerProfileSample{}).Count(&sampleCount).Error)
	require.EqualValues(t, 0, profileCount)
	require.EqualValues(t, 0, sampleCount)
}

func TestSpeakerProfileListNeverEmitsEmbeddings(t *testing.T) {
	handler, db := newSpeakerProfileTestHandler(t)
	created := performSpeakerProfileRequest(t, http.MethodPost, map[string]any{"name": "Ada", "notes": "test subject"}, nil, handler.CreateSpeakerProfile)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var profile models.SpeakerProfile
	require.NoError(t, db.Where("name = ?", "Ada").First(&profile).Error)
	seedSpeakerProfileSample(t, db, profile.ID, "job-a", "SPEAKER_00")

	list := performSpeakerProfileRequest(t, http.MethodGet, nil, nil, handler.ListSpeakerProfiles)
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	var decoded any
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &decoded))
	assertNoForbiddenKeyDeep(t, decoded, "embedding", "embeddings", "Embedding")

	var profiles []map[string]any
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &profiles))
	require.Len(t, profiles, 1)
	for key := range profiles[0] {
		require.Contains(t, []string{"id", "name", "notes", "sample_count", "updated_at"}, key)
	}
}

func TestSpeakerProfileSampleSurvivesJobDeletion(t *testing.T) {
	handler, db := newSpeakerProfileTestHandler(t)
	created := performSpeakerProfileRequest(t, http.MethodPost, map[string]any{"name": "Ada"}, nil, handler.CreateSpeakerProfile)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var profile models.SpeakerProfile
	require.NoError(t, db.Where("name = ?", "Ada").First(&profile).Error)

	job := models.TranscriptionJob{ID: "dangling-job-id", AudioPath: "/srv/audio/sample.wav"}
	require.NoError(t, db.Create(&job).Error)
	sample := seedSpeakerProfileSample(t, db, profile.ID, job.ID, "SPEAKER_00")

	require.NoError(t, db.Unscoped().Delete(&job).Error)

	var survivor models.SpeakerProfileSample
	require.NoError(t, db.First(&survivor, sample.ID).Error)
	require.Equal(t, "dangling-job-id", survivor.SourceJobID)
}

func TestSpeakerProfileSampleDeleteRemovesOnlyThatSample(t *testing.T) {
	handler, db := newSpeakerProfileTestHandler(t)
	created := performSpeakerProfileRequest(t, http.MethodPost, map[string]any{"name": "Ada"}, nil, handler.CreateSpeakerProfile)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var profile models.SpeakerProfile
	require.NoError(t, db.Where("name = ?", "Ada").First(&profile).Error)
	victim := seedSpeakerProfileSample(t, db, profile.ID, "job-a", "SPEAKER_00")
	keeper := seedSpeakerProfileSample(t, db, profile.ID, "job-b", "SPEAKER_01")

	params := gin.Params{
		{Key: "id", Value: fmt.Sprint(profile.ID)},
		{Key: "sample_id", Value: fmt.Sprint(victim.ID)},
	}
	deleted := performSpeakerProfileRequest(t, http.MethodDelete, nil, params, handler.DeleteSpeakerProfileSample)
	require.True(t, deleted.Code == http.StatusOK || deleted.Code == http.StatusNoContent, deleted.Body.String())

	var samples []models.SpeakerProfileSample
	require.NoError(t, db.Find(&samples).Error)
	require.Len(t, samples, 1)
	require.Equal(t, keeper.ID, samples[0].ID)
	var profileCount int64
	require.NoError(t, db.Model(&models.SpeakerProfile{}).Count(&profileCount).Error)
	require.EqualValues(t, 1, profileCount)
}

func TestSpeakerProfileSampleListNeverEmitsEmbeddings(t *testing.T) {
	handler, db := newSpeakerProfileTestHandler(t)
	created := performSpeakerProfileRequest(t, http.MethodPost, map[string]any{"name": "Ada"}, nil, handler.CreateSpeakerProfile)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var profile models.SpeakerProfile
	require.NoError(t, db.Where("name = ?", "Ada").First(&profile).Error)
	seedSpeakerProfileSample(t, db, profile.ID, "job-a", "SPEAKER_00")
	seedSpeakerProfileSample(t, db, profile.ID, "job-b", "SPEAKER_01")

	params := gin.Params{{Key: "id", Value: fmt.Sprint(profile.ID)}}
	list := performSpeakerProfileRequest(t, http.MethodGet, nil, params, handler.ListSpeakerProfileSamples)
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())

	var decoded any
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &decoded))
	assertNoForbiddenKeyDeep(t, decoded, "embedding", "embeddings", "dimensions", "Embedding")

	var samples []map[string]any
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &samples))
	require.Len(t, samples, 2)
	for _, sample := range samples {
		for key := range sample {
			require.Contains(t, []string{"id", "source", "source_speaker_label", "seconds_used", "created_at"}, key)
		}
	}
}
