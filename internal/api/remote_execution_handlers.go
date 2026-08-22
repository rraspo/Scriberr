package api

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"scriberr/internal/remotehealth"

	"github.com/gin-gonic/gin"
)

const remoteHealthMaxTimeout = 5 * time.Second

// RemoteExecutionHealthResponse reports the last known reachability of the
// remote execution host set. Reachability is an observation from a real
// contact (a job run or an explicit check), never a background probe: probing
// on page load or on a poll can wake a sleeping wake-on-LAN GPU host, so the
// passive endpoint reports staleness instead of refreshing on its own.
type RemoteExecutionHealthResponse struct {
	HasRemote bool       `json:"has_remote"`
	Known     bool       `json:"known"`
	Reachable bool       `json:"reachable"`
	Source    string     `json:"source,omitempty"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
}

func remoteHealthResponseFromObservation(hasRemote bool) RemoteExecutionHealthResponse {
	response := RemoteExecutionHealthResponse{HasRemote: hasRemote}
	if observation, known := remotehealth.Last(); known {
		response.Known = true
		response.Reachable = observation.Reachable
		response.Source = observation.Source
		checkedAt := observation.At
		response.CheckedAt = &checkedAt
	}
	return response
}

func (h *Handler) hasRemoteProfiles(c *gin.Context) (bool, []remoteEndpoint, bool) {
	profiles, _, err := h.profileRepo.List(c.Request.Context(), 0, 1000)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list profiles"})
		return false, nil, false
	}

	endpoints := make(map[string]remoteEndpoint)
	for _, profile := range profiles {
		if profile.ExecutionMode != "remote" || strings.TrimSpace(profile.RemoteHost) == "" {
			continue
		}
		port := profile.RemotePort
		if port == 0 {
			port = 22
		}
		timeout := time.Duration(profile.RemoteConnectTimeoutSeconds) * time.Second
		if timeout <= 0 || timeout > remoteHealthMaxTimeout {
			timeout = remoteHealthMaxTimeout
		}
		key := net.JoinHostPort(profile.RemoteHost, fmt.Sprintf("%d", port))
		endpoints[key] = remoteEndpoint{host: profile.RemoteHost, port: port, timeout: timeout}
	}

	targets := make([]remoteEndpoint, 0, len(endpoints))
	for _, target := range endpoints {
		targets = append(targets, target)
	}
	return len(targets) > 0, targets, true
}

type remoteEndpoint struct {
	host    string
	port    int
	timeout time.Duration
}

// RemoteExecutionHealth reports the last known remote reachability without
// contacting the remote host.
// @Summary Remote execution health
// @Description Report the last known reachability of the remote SSH hosts referenced by remote-mode profiles, without contacting them
// @Tags transcription
// @Produce json
// @Success 200 {object} RemoteExecutionHealthResponse
// @Router /api/v1/transcription/remote-execution/health [get]
// @Security ApiKeyAuth
// @Security BearerAuth
func (h *Handler) RemoteExecutionHealth(c *gin.Context) {
	hasRemote, _, ok := h.hasRemoteProfiles(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, remoteHealthResponseFromObservation(hasRemote))
}

// RemoteExecutionHealthCheck probes the remote hosts once, on explicit
// request, and records the result as the current observation.
// @Summary Check remote execution health now
// @Description Probe the remote SSH hosts referenced by remote-mode profiles once and record the observation
// @Tags transcription
// @Produce json
// @Success 200 {object} RemoteExecutionHealthResponse
// @Router /api/v1/transcription/remote-execution/health/check [post]
// @Security ApiKeyAuth
// @Security BearerAuth
func (h *Handler) RemoteExecutionHealthCheck(c *gin.Context) {
	hasRemote, targets, ok := h.hasRemoteProfiles(c)
	if !ok {
		return
	}
	if !hasRemote {
		c.JSON(http.StatusOK, RemoteExecutionHealthResponse{HasRemote: false})
		return
	}

	allReachable := true
	var mutex sync.Mutex
	var waitGroup sync.WaitGroup
	for _, target := range targets {
		waitGroup.Add(1)
		go func(target remoteEndpoint) {
			defer waitGroup.Done()
			reachable := sshEndpointReachable(target.host, target.port, target.timeout)
			mutex.Lock()
			if !reachable {
				allReachable = false
			}
			mutex.Unlock()
		}(target)
	}
	waitGroup.Wait()

	remotehealth.Record(allReachable, "check")
	c.JSON(http.StatusOK, remoteHealthResponseFromObservation(true))
}

// sshEndpointReachable dials the endpoint and confirms it greets with an SSH
// banner, so an unrelated service listening on the port does not count as a
// live SSH host.
func sshEndpointReachable(host string, port int, timeout time.Duration) bool {
	address := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	connection, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return false
	}
	defer connection.Close()
	if err := connection.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return false
	}
	banner := make([]byte, 4)
	if _, err := io.ReadFull(connection, banner); err != nil {
		return false
	}
	return string(banner) == "SSH-"
}
