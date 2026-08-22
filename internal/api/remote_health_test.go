package api

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"scriberr/internal/models"
	"scriberr/internal/repository"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newRemoteHealthTestHandler(t *testing.T) (*Handler, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.TranscriptionProfile{}))
	return &Handler{profileRepo: repository.NewProfileRepository(db)}, db
}

func performRemoteHealthRequest(t *testing.T, handler *Handler) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/remote-execution/health", nil)
	handler.RemoteExecutionHealth(context)
	return recorder
}

// startFakeSSHServer listens on a loopback port and writes an SSH banner to
// every connection, imitating the only part of an SSH server the health check
// reads.
func startFakeSSHServer(t *testing.T) (host string, port int) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("SSH-2.0-FakeServer\r\n"))
			_ = conn.Close()
		}
	}()
	addr := listener.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port
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

type remoteHealthResponse struct {
	HasRemote bool `json:"has_remote"`
	Reachable bool `json:"reachable"`
	Hosts     []struct {
		Host      string `json:"host"`
		Port      int    `json:"port"`
		Reachable bool   `json:"reachable"`
	} `json:"hosts"`
}

func TestRemoteExecutionHealthReachable(t *testing.T) {
	handler, db := newRemoteHealthTestHandler(t)
	host, port := startFakeSSHServer(t)
	createRemoteHealthProfile(t, db, "reachable-profile", host, port)

	response := performRemoteHealthRequest(t, handler)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body remoteHealthResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.True(t, body.HasRemote)
	require.True(t, body.Reachable)
	require.Len(t, body.Hosts, 1)
	require.True(t, body.Hosts[0].Reachable)
}

func TestRemoteExecutionHealthUnreachable(t *testing.T) {
	handler, db := newRemoteHealthTestHandler(t)
	// Grab a loopback port and close it again so nothing is listening there.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	closedPort := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	createRemoteHealthProfile(t, db, "unreachable-profile", "127.0.0.1", closedPort)

	response := performRemoteHealthRequest(t, handler)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body remoteHealthResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.True(t, body.HasRemote)
	require.False(t, body.Reachable)
}

func TestRemoteExecutionHealthNonSSHServiceIsUnreachable(t *testing.T) {
	handler, db := newRemoteHealthTestHandler(t)
	// An HTTP server accepts TCP connections but never sends an SSH banner.
	httpServer := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(httpServer.Close)
	addr := strings.TrimPrefix(httpServer.URL, "http://")
	host, portText, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	createRemoteHealthProfile(t, db, "non-ssh-profile", host, port)

	response := performRemoteHealthRequest(t, handler)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body remoteHealthResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.True(t, body.HasRemote)
	require.False(t, body.Reachable)
}

func TestRemoteExecutionHealthWithoutRemoteProfiles(t *testing.T) {
	handler, db := newRemoteHealthTestHandler(t)
	require.NoError(t, db.Create(&models.TranscriptionProfile{
		ID: "local-profile", Name: "Local", ExecutionMode: "local",
	}).Error)

	response := performRemoteHealthRequest(t, handler)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body remoteHealthResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.False(t, body.HasRemote)
	require.False(t, body.Reachable)
	require.Empty(t, body.Hosts)
}
