package api

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"scriberr/internal/models"
	"scriberr/internal/remotehealth"
	"scriberr/internal/repository"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newRemoteHealthTestHandler(t *testing.T) (*Handler, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	remotehealth.Reset()
	t.Cleanup(remotehealth.Reset)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.TranscriptionProfile{}))
	return &Handler{profileRepo: repository.NewProfileRepository(db)}, db
}

func performRemoteHealthRequest(t *testing.T, handler *Handler, method string, handlerFunc gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	requestContext, _ := gin.CreateTestContext(recorder)
	requestContext.Request = httptest.NewRequest(method, "/remote-execution/health", nil)
	handlerFunc(requestContext)
	return recorder
}

// startFakeSSHServer listens on a loopback port, writes an SSH banner to every
// connection, and counts how many connections it ever received - the count is
// what proves the passive endpoint generates no traffic.
func startFakeSSHServer(t *testing.T) (host string, port int, connections *atomic.Int64) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	connections = &atomic.Int64{}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connections.Add(1)
			_, _ = conn.Write([]byte("SSH-2.0-FakeServer\r\n"))
			_ = conn.Close()
		}
	}()
	addr := listener.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port, connections
}

func createRemoteHealthProfile(t *testing.T, db *gorm.DB, id, host string, port int) {
	t.Helper()
	require.NoError(t, db.Create(&models.TranscriptionProfile{
		ID: id, Name: id, ExecutionMode: "remote",
		RemoteHost: host, RemotePort: port,
		RemoteUser: "scriberr", RemoteKeyPath: "/etc/scriberr/keys/id_ed25519",
		RemoteConnectTimeoutSeconds: 2,
	}).Error)
}

type remoteHealthTestResponse struct {
	HasRemote bool       `json:"has_remote"`
	Known     bool       `json:"known"`
	Reachable bool       `json:"reachable"`
	Source    string     `json:"source"`
	CheckedAt *time.Time `json:"checked_at"`
}

func decodeRemoteHealthResponse(t *testing.T, recorder *httptest.ResponseRecorder) remoteHealthTestResponse {
	t.Helper()
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var body remoteHealthTestResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	return body
}

func TestRemoteExecutionHealthNeverContactsTheRemoteHost(t *testing.T) {
	handler, db := newRemoteHealthTestHandler(t)
	host, port, connections := startFakeSSHServer(t)
	createRemoteHealthProfile(t, db, "passive-profile", host, port)

	body := decodeRemoteHealthResponse(t, performRemoteHealthRequest(t, handler, http.MethodGet, handler.RemoteExecutionHealth))
	require.True(t, body.HasRemote)
	require.False(t, body.Known, "no observation exists yet, and the passive endpoint must not create one")
	require.Nil(t, body.CheckedAt)
	require.EqualValues(t, 0, connections.Load(), "the passive endpoint must never dial the remote host")
}

func TestRemoteExecutionHealthCheckProbesOnceAndRecords(t *testing.T) {
	handler, db := newRemoteHealthTestHandler(t)
	host, port, connections := startFakeSSHServer(t)
	createRemoteHealthProfile(t, db, "check-profile", host, port)

	checked := decodeRemoteHealthResponse(t, performRemoteHealthRequest(t, handler, http.MethodPost, handler.RemoteExecutionHealthCheck))
	require.True(t, checked.HasRemote)
	require.True(t, checked.Known)
	require.True(t, checked.Reachable)
	require.Equal(t, "check", checked.Source)
	require.NotNil(t, checked.CheckedAt)
	require.EqualValues(t, 1, connections.Load())

	passive := decodeRemoteHealthResponse(t, performRemoteHealthRequest(t, handler, http.MethodGet, handler.RemoteExecutionHealth))
	require.True(t, passive.Known)
	require.True(t, passive.Reachable)
	require.Equal(t, "check", passive.Source)
	require.EqualValues(t, 1, connections.Load(), "reading the passive endpoint after a check must not dial again")
}

func TestRemoteExecutionHealthCheckUnreachableHostRecordsUnreachable(t *testing.T) {
	handler, db := newRemoteHealthTestHandler(t)
	// A closed port on loopback refuses immediately.
	closedListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	closedAddr := closedListener.Addr().(*net.TCPAddr)
	require.NoError(t, closedListener.Close())
	createRemoteHealthProfile(t, db, "unreachable-profile", closedAddr.IP.String(), closedAddr.Port)

	checked := decodeRemoteHealthResponse(t, performRemoteHealthRequest(t, handler, http.MethodPost, handler.RemoteExecutionHealthCheck))
	require.True(t, checked.Known)
	require.False(t, checked.Reachable)
	require.Equal(t, "check", checked.Source)
}

func TestRemoteExecutionHealthSurfacesJobObservations(t *testing.T) {
	handler, db := newRemoteHealthTestHandler(t)
	host, port, connections := startFakeSSHServer(t)
	createRemoteHealthProfile(t, db, "job-observed-profile", host, port)

	remotehealth.Record(false, "job")

	body := decodeRemoteHealthResponse(t, performRemoteHealthRequest(t, handler, http.MethodGet, handler.RemoteExecutionHealth))
	require.True(t, body.Known)
	require.False(t, body.Reachable)
	require.Equal(t, "job", body.Source)
	require.EqualValues(t, 0, connections.Load())
}

func TestRemoteExecutionHealthWithoutRemoteProfiles(t *testing.T) {
	handler, _ := newRemoteHealthTestHandler(t)

	body := decodeRemoteHealthResponse(t, performRemoteHealthRequest(t, handler, http.MethodGet, handler.RemoteExecutionHealth))
	require.False(t, body.HasRemote)

	checked := decodeRemoteHealthResponse(t, performRemoteHealthRequest(t, handler, http.MethodPost, handler.RemoteExecutionHealthCheck))
	require.False(t, checked.HasRemote)
	require.False(t, checked.Known)
}
