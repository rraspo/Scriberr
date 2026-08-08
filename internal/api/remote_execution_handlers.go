package api

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const remoteHealthMaxTimeout = 5 * time.Second

// RemoteHostHealth reports reachability of one distinct remote SSH endpoint.
type RemoteHostHealth struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Reachable bool   `json:"reachable"`
}

// RemoteExecutionHealthResponse reports whether remote execution is currently
// available. Reachable is true only when every distinct remote endpoint
// referenced by a remote-mode profile answers with an SSH banner.
type RemoteExecutionHealthResponse struct {
	HasRemote bool               `json:"has_remote"`
	Reachable bool               `json:"reachable"`
	Hosts     []RemoteHostHealth `json:"hosts"`
}

// @Summary Remote execution health
// @Description Check whether the remote SSH hosts referenced by remote-mode profiles are reachable
// @Tags transcription
// @Produce json
// @Success 200 {object} RemoteExecutionHealthResponse
// @Router /api/v1/transcription/remote-execution/health [get]
// @Security ApiKeyAuth
// @Security BearerAuth
func (h *Handler) RemoteExecutionHealth(c *gin.Context) {
	profiles, _, err := h.profileRepo.List(c.Request.Context(), 0, 1000)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list profiles"})
		return
	}

	type endpoint struct {
		host    string
		port    int
		timeout time.Duration
	}
	endpoints := make(map[string]endpoint)
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
		endpoints[key] = endpoint{host: profile.RemoteHost, port: port, timeout: timeout}
	}

	response := RemoteExecutionHealthResponse{Hosts: []RemoteHostHealth{}}
	if len(endpoints) == 0 {
		c.JSON(http.StatusOK, response)
		return
	}
	response.HasRemote = true

	var mutex sync.Mutex
	var waitGroup sync.WaitGroup
	for _, target := range endpoints {
		waitGroup.Add(1)
		go func(target endpoint) {
			defer waitGroup.Done()
			reachable := sshEndpointReachable(target.host, target.port, target.timeout)
			mutex.Lock()
			response.Hosts = append(response.Hosts, RemoteHostHealth{
				Host: target.host, Port: target.port, Reachable: reachable,
			})
			mutex.Unlock()
		}(target)
	}
	waitGroup.Wait()

	response.Reachable = true
	for _, host := range response.Hosts {
		if !host.Reachable {
			response.Reachable = false
			break
		}
	}
	c.JSON(http.StatusOK, response)
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
