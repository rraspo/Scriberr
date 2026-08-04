package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"scriberr/internal/models"
	"scriberr/internal/repository"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newProfileTestHandler(t *testing.T) (*Handler, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.TranscriptionProfile{}))
	return &Handler{profileRepo: repository.NewProfileRepository(db)}, db
}

func performProfileRequest(t *testing.T, method, path string, body any, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		require.NoError(t, err)
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(method, path, bytes.NewReader(encoded))
	context.Request.Header.Set("Content-Type", "application/json")
	if id := strings.TrimPrefix(path, "/profiles/"); id != path {
		context.Params = gin.Params{{Key: "id", Value: id}}
	}
	handler(context)
	return recorder
}

func TestProfileAPIRoundTripRemoteConnectionFields(t *testing.T) {
	handler, _ := newProfileTestHandler(t)
	payload := map[string]any{
		"name": "Remote GPU", "execution_mode": "remote", "remote_host": "gpu-host.example",
		"remote_port": 2202, "remote_user": "scriberr", "remote_key_path": "/etc/scriberr/keys/id_ed25519",
		"remote_work_dir": "/srv/scriberr-work", "remote_connect_timeout_seconds": 25,
		"remote_command_prefix": "wsl -d Ubuntu --",
	}
	created := performProfileRequest(t, http.MethodPost, "/profiles", payload, handler.CreateProfile)
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())
	var createdProfile models.TranscriptionProfile
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &createdProfile))

	read := performProfileRequest(t, http.MethodGet, "/profiles/"+createdProfile.ID, nil, handler.GetProfile)
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	var profile models.TranscriptionProfile
	require.NoError(t, json.Unmarshal(read.Body.Bytes(), &profile))
	require.Equal(t, "remote", profile.ExecutionMode)
	require.Equal(t, "gpu-host.example", profile.RemoteHost)
	require.Equal(t, 2202, profile.RemotePort)
	require.Equal(t, "scriberr", profile.RemoteUser)
	require.Equal(t, "/etc/scriberr/keys/id_ed25519", profile.RemoteKeyPath)
	require.Equal(t, "/srv/scriberr-work", profile.RemoteWorkDir)
	require.Equal(t, 25, profile.RemoteConnectTimeoutSeconds)
	require.Equal(t, "wsl -d Ubuntu --", profile.RemoteCommandPrefix)
}

func TestProfileAPIExposesKeyPathButNeverPrivateKeyMaterial(t *testing.T) {
	handler, db := newProfileTestHandler(t)
	const privateKeyMaterial = "PRIVATE-KEY-MATERIAL-MUST-NOT-SURVIVE"
	payload := map[string]any{
		"name": "Safe Remote", "execution_mode": "remote", "remote_host": "gpu-host.example",
		"remote_user": "scriberr", "remote_key_path": "/etc/scriberr/keys/id_ed25519",
		"remote_private_key": privateKeyMaterial,
	}
	response := performProfileRequest(t, http.MethodPost, "/profiles", payload, handler.CreateProfile)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"remote_key_path":"/etc/scriberr/keys/id_ed25519"`)
	require.NotContains(t, response.Body.String(), privateKeyMaterial)
	require.NotContains(t, response.Body.String(), "remote_private_key")

	modelType := reflect.TypeOf(models.TranscriptionProfile{})
	for i := 0; i < modelType.NumField(); i++ {
		name := strings.ToLower(modelType.Field(i).Name)
		require.False(t, strings.Contains(name, "privatekey") || strings.Contains(name, "keymaterial"), name)
	}
	columnTypes, err := db.Migrator().ColumnTypes(&models.TranscriptionProfile{})
	require.NoError(t, err)
	for _, column := range columnTypes {
		name := strings.ToLower(column.Name())
		require.False(t, strings.Contains(name, "private_key") || strings.Contains(name, "key_material"), name)
	}
}

func TestProfileAPIRemoteValidationAndDefaults(t *testing.T) {
	tests := []struct {
		name, omitted, wantMessage string
	}{
		{name: "missing host", omitted: "remote_host", wantMessage: "remote_host is required"},
		{name: "missing user", omitted: "remote_user", wantMessage: "remote_user is required"},
		{name: "missing key path", omitted: "remote_key_path", wantMessage: "remote_key_path is required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, _ := newProfileTestHandler(t)
			payload := map[string]any{
				"name": "Invalid Remote", "execution_mode": "remote", "remote_host": "gpu-host.example",
				"remote_user": "scriberr", "remote_key_path": "/etc/scriberr/keys/id_ed25519",
			}
			delete(payload, test.omitted)
			response := performProfileRequest(t, http.MethodPost, "/profiles", payload, handler.CreateProfile)
			require.Equal(t, http.StatusBadRequest, response.Code)
			require.Contains(t, response.Body.String(), test.wantMessage)
		})
	}
	t.Run("invalid execution mode", func(t *testing.T) {
		handler, _ := newProfileTestHandler(t)
		payload := map[string]any{"name": "Invalid Mode", "execution_mode": "elsewhere"}
		response := performProfileRequest(t, http.MethodPost, "/profiles", payload, handler.CreateProfile)
		require.Equal(t, http.StatusBadRequest, response.Code)
		require.Contains(t, response.Body.String(), "execution_mode must be either local or remote")
	})

	handler, _ := newProfileTestHandler(t)
	payload := map[string]any{
		"name": "Defaulted Remote", "execution_mode": "remote", "remote_host": "gpu-host.example",
		"remote_user": "scriberr", "remote_key_path": "/etc/scriberr/keys/id_ed25519",
	}
	response := performProfileRequest(t, http.MethodPost, "/profiles", payload, handler.CreateProfile)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var profile models.TranscriptionProfile
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &profile))
	require.Equal(t, 22, profile.RemotePort)
	require.Equal(t, 10, profile.RemoteConnectTimeoutSeconds)
}

func TestUpdateProfileRejectsIncompleteRemoteConnection(t *testing.T) {
	handler, db := newProfileTestHandler(t)
	profile := models.TranscriptionProfile{Name: "Local"}
	require.NoError(t, db.Create(&profile).Error)
	payload := map[string]any{"name": "Remote", "execution_mode": "remote"}

	response := performProfileRequest(t, http.MethodPut, "/profiles/"+profile.ID, payload, handler.UpdateProfile)

	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Contains(t, response.Body.String(), "remote_host is required")
}

func TestRemoteProfilePreservesSpeakerCaps(t *testing.T) {
	handler, _ := newProfileTestHandler(t)
	created := performProfileRequest(t, http.MethodPost, "/profiles", map[string]any{
		"name":       "Speaker Limits",
		"parameters": map[string]any{"min_speakers": 2, "max_speakers": 6},
	}, handler.CreateProfile)
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())
	var profile models.TranscriptionProfile
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &profile))

	updated := performProfileRequest(t, http.MethodPut, "/profiles/"+profile.ID, map[string]any{
		"name": "Speaker Limits", "execution_mode": "remote", "remote_host": "gpu-host.example",
		"remote_user": "scriberr", "remote_key_path": "/etc/scriberr/keys/id_ed25519",
	}, handler.UpdateProfile)
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	require.NoError(t, json.Unmarshal(updated.Body.Bytes(), &profile))
	require.NotNil(t, profile.Parameters.MinSpeakers)
	require.NotNil(t, profile.Parameters.MaxSpeakers)
	require.Equal(t, 2, *profile.Parameters.MinSpeakers)
	require.Equal(t, 6, *profile.Parameters.MaxSpeakers)
}
