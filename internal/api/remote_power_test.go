package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"scriberr/internal/config"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// fakeMissionControl imitates the two Mission Control endpoints the power
// buttons depend on: starting a runbook run and reading a run's status and
// output.
type fakeMissionControl struct {
	mu          sync.Mutex
	startedPath string
	startStatus int
	runStatus   string
	output      string
}

func (f *fakeMissionControl) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/runbooks/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.startedPath = r.Method + " " + r.URL.Path
		if f.startStatus != 0 && f.startStatus != http.StatusCreated {
			w.WriteHeader(f.startStatus)
			_, _ = w.Write([]byte(`{"error":"runbook refused"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"run_id":"run-123"}`))
	})
	mux.HandleFunc("/api/runs/run-123", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "run-123", "status": f.runStatus})
	})
	mux.HandleFunc("/api/runs/run-123/output", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(f.output))
	})
	return mux
}

const (
	testWakeRunbook  = "lab/gpu-host/wake"
	testSleepRunbook = "lab/gpu-host/sleep"
)

func newPowerTestHandler(missionControlURL string) *Handler {
	gin.SetMode(gin.TestMode)
	return &Handler{config: &config.Config{
		MissionControlURL: missionControlURL,
		PowerWakeRunbook:  testWakeRunbook,
		PowerSleepRunbook: testSleepRunbook,
	}}
}

func performPowerStart(t *testing.T, handler *Handler, action string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/remote-execution/power/"+action, nil)
	context.Params = gin.Params{{Key: "action", Value: action}}
	handler.StartRemotePowerAction(context)
	return recorder
}

func performPowerStatus(t *testing.T, handler *Handler, runID string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/remote-execution/power/runs/"+runID, nil)
	context.Params = gin.Params{{Key: "run_id", Value: runID}}
	handler.GetRemotePowerRun(context)
	return recorder
}

type powerStatusBody struct {
	Status  string `json:"status"`
	Done    bool   `json:"done"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func decodeBody[T any](t *testing.T, recorder *httptest.ResponseRecorder) T {
	t.Helper()
	var body T
	raw, err := io.ReadAll(recorder.Body)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &body), string(raw))
	return body
}

func TestStartRemotePowerActionStartsTheMatchingRunbook(t *testing.T) {
	for action, runbook := range map[string]string{"wake": testWakeRunbook, "sleep": testSleepRunbook} {
		t.Run(action, func(t *testing.T) {
			fake := &fakeMissionControl{}
			server := httptest.NewServer(fake.handler())
			defer server.Close()

			recorder := performPowerStart(t, newPowerTestHandler(server.URL), action)

			require.Equal(t, http.StatusAccepted, recorder.Code, recorder.Body.String())
			body := decodeBody[map[string]string](t, recorder)
			require.Equal(t, "run-123", body["run_id"])
			require.Equal(t, "POST /api/runbooks/"+runbook+"/runs", fake.startedPath)
		})
	}
}

func TestStartRemotePowerActionWithoutARunbookConfigured(t *testing.T) {
	fake := &fakeMissionControl{}
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	handler := newPowerTestHandler(server.URL)
	handler.config.PowerSleepRunbook = ""
	recorder := performPowerStart(t, handler, "sleep")

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Empty(t, fake.startedPath)
}

func TestStartRemotePowerActionRejectsUnknownActions(t *testing.T) {
	fake := &fakeMissionControl{}
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	recorder := performPowerStart(t, newPowerTestHandler(server.URL), "reboot")

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Empty(t, fake.startedPath, "no runbook may start for an unknown action")
}

func TestStartRemotePowerActionWithoutMissionControlConfigured(t *testing.T) {
	recorder := performPowerStart(t, newPowerTestHandler(""), "wake")

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	body := decodeBody[map[string]string](t, recorder)
	require.NotEmpty(t, body["error"])
}

func TestStartRemotePowerActionWhenMissionControlIsUnreachable(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	unreachableURL := server.URL
	server.Close()

	recorder := performPowerStart(t, newPowerTestHandler(unreachableURL), "wake")

	require.Equal(t, http.StatusBadGateway, recorder.Code)
	body := decodeBody[map[string]string](t, recorder)
	require.NotEmpty(t, body["error"])
}

func TestStartRemotePowerActionWhenMissionControlRefuses(t *testing.T) {
	fake := &fakeMissionControl{startStatus: http.StatusInternalServerError}
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	recorder := performPowerStart(t, newPowerTestHandler(server.URL), "sleep")

	require.Equal(t, http.StatusBadGateway, recorder.Code)
	body := decodeBody[map[string]string](t, recorder)
	require.NotEmpty(t, body["error"])
}

func TestGetRemotePowerRunWhileRunning(t *testing.T) {
	fake := &fakeMissionControl{runStatus: "running"}
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	recorder := performPowerStatus(t, newPowerTestHandler(server.URL), "run-123")

	require.Equal(t, http.StatusOK, recorder.Code)
	body := decodeBody[powerStatusBody](t, recorder)
	require.Equal(t, "running", body.Status)
	require.False(t, body.Done)
	require.False(t, body.OK)
}

func TestGetRemotePowerRunCompletedReportsLastOutputLine(t *testing.T) {
	fake := &fakeMissionControl{
		runStatus: "completed",
		output:    "target: lab/gpu-host\n\n=== polling ===\ngpu host answers on port 22 after 9s\n\n",
	}
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	recorder := performPowerStatus(t, newPowerTestHandler(server.URL), "run-123")

	require.Equal(t, http.StatusOK, recorder.Code)
	body := decodeBody[powerStatusBody](t, recorder)
	require.True(t, body.Done)
	require.True(t, body.OK)
	require.Equal(t, "gpu host answers on port 22 after 9s", body.Message)
}

func TestGetRemotePowerRunFailedSurfacesTheRunbookReason(t *testing.T) {
	fake := &fakeMissionControl{
		runStatus: "failed",
		output:    "=== checking ===\na Scriberr transcription is running on the gpu host; not suspending. Try again when it finishes.\n",
	}
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	recorder := performPowerStatus(t, newPowerTestHandler(server.URL), "run-123")

	require.Equal(t, http.StatusOK, recorder.Code)
	body := decodeBody[powerStatusBody](t, recorder)
	require.True(t, body.Done)
	require.False(t, body.OK)
	require.Equal(t, "a Scriberr transcription is running on the gpu host; not suspending. Try again when it finishes.", body.Message)
}

func TestGetRemotePowerRunRejectsMalformedRunIDs(t *testing.T) {
	fake := &fakeMissionControl{runStatus: "running"}
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	recorder := performPowerStatus(t, newPowerTestHandler(server.URL), "../servers")

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestCompletedWakeExpiresTheRemoteHealthCache(t *testing.T) {
	fake := &fakeMissionControl{runStatus: "completed", output: "gpu host answers on port 22 after 4s\n"}
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	handler := newPowerTestHandler(server.URL)
	handler.remoteHealthCache.response = RemoteExecutionHealthResponse{HasRemote: true, Reachable: false}
	handler.remoteHealthCache.fetchedAt = time.Now()

	recorder := performPowerStatus(t, handler, "run-123")
	require.Equal(t, http.StatusOK, recorder.Code)

	handler.remoteHealthCache.Lock()
	defer handler.remoteHealthCache.Unlock()
	require.True(t, handler.remoteHealthCache.fetchedAt.IsZero(), "a finished run must expire the cached reachability answer so the next health poll probes again")
}
