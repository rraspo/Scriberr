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
	"scriberr/internal/queue"
	"scriberr/internal/repository"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newStartTranscriptionTestHandler(t *testing.T) (*Handler, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.TranscriptionProfile{}, &models.TranscriptionJob{}))
	jobRepo := repository.NewJobRepository(db)
	return &Handler{
		jobRepo:     jobRepo,
		profileRepo: repository.NewProfileRepository(db),
		taskQueue:   queue.NewTaskQueue(1, nil, jobRepo),
	}, db
}

func performStartRequest(t *testing.T, handler *Handler, jobID string, query string, params models.WhisperXParams) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(params)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/transcription/"+jobID+"/start"+query, bytes.NewReader(encoded))
	context.Request.Header.Set("Content-Type", "application/json")
	context.Params = gin.Params{{Key: "id", Value: jobID}}
	handler.StartTranscription(context)
	return recorder
}

func createRemoteProfile(t *testing.T, db *gorm.DB) *models.TranscriptionProfile {
	t.Helper()
	profile := &models.TranscriptionProfile{
		ID:                          "remote-profile",
		Name:                        "Remote GPU",
		ExecutionMode:               "remote",
		RemoteHost:                  "gpu-host.example",
		RemotePort:                  2202,
		RemoteUser:                  "scriberr",
		RemoteKeyPath:               "/etc/scriberr/keys/id_ed25519",
		RemoteWorkDir:               "/srv/scriberr-work",
		RemoteCommandPrefix:         "wsl -d Ubuntu --",
		RemoteConnectTimeoutSeconds: 25,
	}
	require.NoError(t, db.Create(profile).Error)
	return profile
}

func createUploadedJob(t *testing.T, db *gorm.DB, jobID string) {
	t.Helper()
	require.NoError(t, db.Create(&models.TranscriptionJob{
		ID:        jobID,
		AudioPath: "/data/uploads/" + jobID + ".wav",
		Status:    models.StatusUploaded,
	}).Error)
}

func TestStartTranscriptionWithProfileSnapshotsExecution(t *testing.T) {
	handler, db := newStartTranscriptionTestHandler(t)
	profile := createRemoteProfile(t, db)
	createUploadedJob(t, db, "job-remote")

	response := performStartRequest(t, handler, "job-remote", "?profile_id="+profile.ID, profile.Parameters)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var job models.TranscriptionJob
	require.NoError(t, db.First(&job, "id = ?", "job-remote").Error)
	require.Equal(t, "remote", job.Execution.Mode)
	require.Equal(t, "gpu-host.example", job.Execution.RemoteHost)
	require.Equal(t, 2202, job.Execution.RemotePort)
	require.Equal(t, "scriberr", job.Execution.RemoteUser)
	require.Equal(t, "/etc/scriberr/keys/id_ed25519", job.Execution.RemoteKeyPath)
	require.Equal(t, "/srv/scriberr-work", job.Execution.RemoteWorkDir)
	require.Equal(t, "wsl -d Ubuntu --", job.Execution.RemoteCommandPrefix)
	require.Equal(t, 25, job.Execution.ConnectTimeoutSeconds)
}

func TestStartTranscriptionWithoutProfileResetsExecutionToLocal(t *testing.T) {
	handler, db := newStartTranscriptionTestHandler(t)
	profile := createRemoteProfile(t, db)
	createUploadedJob(t, db, "job-reset")

	started := performStartRequest(t, handler, "job-reset", "?profile_id="+profile.ID, profile.Parameters)
	require.Equal(t, http.StatusOK, started.Code, started.Body.String())

	require.NoError(t, db.Model(&models.TranscriptionJob{}).
		Where("id = ?", "job-reset").
		Update("status", models.StatusFailed).Error)

	restarted := performStartRequest(t, handler, "job-reset", "", models.WhisperXParams{Model: "small", Device: "cpu"})
	require.Equal(t, http.StatusOK, restarted.Code, restarted.Body.String())

	var job models.TranscriptionJob
	require.NoError(t, db.First(&job, "id = ?", "job-reset").Error)
	require.Equal(t, "local", job.Execution.Mode)
	require.Empty(t, job.Execution.RemoteHost)
}

func TestStartTranscriptionWithProfileAndLocalOverrideRunsLocal(t *testing.T) {
	handler, db := newStartTranscriptionTestHandler(t)
	profile := createRemoteProfile(t, db)
	createUploadedJob(t, db, "job-local-override")

	response := performStartRequest(t, handler, "job-local-override",
		"?profile_id="+profile.ID+"&execution_mode=local", profile.Parameters)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var job models.TranscriptionJob
	require.NoError(t, db.First(&job, "id = ?", "job-local-override").Error)
	require.Equal(t, "local", job.Execution.Mode)
}

func TestStartTranscriptionRejectsInvalidExecutionModeOverride(t *testing.T) {
	handler, db := newStartTranscriptionTestHandler(t)
	createUploadedJob(t, db, "job-bad-override")

	response := performStartRequest(t, handler, "job-bad-override",
		"?execution_mode=remote", models.WhisperXParams{Model: "small"})
	require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
}

func TestStartTranscriptionWithUnknownProfileFails(t *testing.T) {
	handler, db := newStartTranscriptionTestHandler(t)
	createUploadedJob(t, db, "job-unknown-profile")

	response := performStartRequest(t, handler, "job-unknown-profile", "?profile_id=does-not-exist", models.WhisperXParams{Model: "small"})
	require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())

	var job models.TranscriptionJob
	require.NoError(t, db.First(&job, "id = ?", "job-unknown-profile").Error)
	require.Equal(t, models.StatusUploaded, job.Status)
}
