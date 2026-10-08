package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	powerHTTPTimeout  = 10 * time.Second
	powerMaxBodyBytes = 1 << 20
)

var powerHTTPClient = &http.Client{Timeout: powerHTTPTimeout}

var powerRunIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

var errMissionControlUnreachable = errors.New("could not reach Mission Control")

func (h *Handler) powerAvailable() bool {
	return h.config != nil && h.config.MissionControlURL != "" &&
		h.config.PowerWakeRunbook != "" && h.config.PowerSleepRunbook != ""
}

// StartRemotePowerAction asks Mission Control to run the configured wake or
// sleep runbook for the GPU host.
// @Summary Start a GPU host power action
// @Tags transcription
// @Produce json
// @Param action path string true "wake or sleep"
// @Success 202 {object} map[string]string
// @Router /api/v1/transcription/remote-execution/power/{action} [post]
// @Security ApiKeyAuth
// @Security BearerAuth
func (h *Handler) StartRemotePowerAction(c *gin.Context) {
	action := c.Param("action")
	if action != "wake" && action != "sleep" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown power action"})
		return
	}

	var runbook string
	if h.config != nil {
		runbook = h.config.PowerWakeRunbook
		if action == "sleep" {
			runbook = h.config.PowerSleepRunbook
		}
	}
	if h.config == nil || h.config.MissionControlURL == "" || runbook == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "GPU host power control is not configured"})
		return
	}

	startURL := strings.TrimRight(h.config.MissionControlURL, "/") + "/api/runbooks/" + runbook + "/runs"
	request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, startURL, bytes.NewReader([]byte(`{"params":{}}`)))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Could not reach Mission Control"})
		return
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := powerHTTPClient.Do(request)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Could not reach Mission Control"})
		return
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, powerMaxBodyBytes))

	if response.StatusCode < 200 || response.StatusCode > 299 {
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("Mission Control refused the %s run (HTTP %d)", action, response.StatusCode)})
		return
	}

	var started struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(body, &started); err != nil || started.RunID == "" {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Mission Control returned no run id"})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"run_id": started.RunID})
}

// GetRemotePowerRun reports the state of a power runbook run and, once it has
// finished, the last line of its output.
// @Summary Get a GPU host power run
// @Tags transcription
// @Produce json
// @Param run_id path string true "Mission Control run id"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/transcription/remote-execution/power/runs/{run_id} [get]
// @Security ApiKeyAuth
// @Security BearerAuth
func (h *Handler) GetRemotePowerRun(c *gin.Context) {
	runID := c.Param("run_id")
	if !powerRunIDPattern.MatchString(runID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid run id"})
		return
	}
	if h.config == nil || h.config.MissionControlURL == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "GPU host power control is not configured"})
		return
	}

	runURL := strings.TrimRight(h.config.MissionControlURL, "/") + "/api/runs/" + url.PathEscape(runID)

	statusBody, err := missionControlGet(c, runURL)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	var run struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(statusBody, &run); err != nil || run.Status == "" {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Mission Control returned an unreadable run status"})
		return
	}

	status := strings.ToLower(run.Status)
	done := status != "running" && status != "queued" && status != "pending"
	result := gin.H{"status": status, "done": done, "ok": status == "completed", "message": ""}

	if done {
		if output, err := missionControlGet(c, runURL+"/output"); err == nil {
			result["message"] = lastNonEmptyLine(string(output))
		}
		h.remoteHealthCache.Lock()
		h.remoteHealthCache.fetchedAt = time.Time{}
		h.remoteHealthCache.Unlock()
	}
	c.JSON(http.StatusOK, result)
}

func missionControlGet(c *gin.Context, target string) ([]byte, error) {
	request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, target, nil)
	if err != nil {
		return nil, errMissionControlUnreachable
	}
	response, err := powerHTTPClient.Do(request)
	if err != nil {
		return nil, errMissionControlUnreachable
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("Mission Control answered HTTP %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, powerMaxBodyBytes))
}

func lastNonEmptyLine(text string) string {
	lines := strings.Split(text, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if line := strings.TrimSpace(lines[index]); line != "" {
			return line
		}
	}
	return ""
}
