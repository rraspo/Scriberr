package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"scriberr/internal/models"
	"scriberr/pkg/logger"

	"github.com/gin-gonic/gin"
)

func (h *Handler) serveVisualImage(c *gin.Context, folder string) {
	job, err := h.jobRepo.FindByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found", "code": "not_found"})
		return
	}
	count := len(job.Frames)
	if folder == "sheets" {
		count = len(job.Sheets)
	}
	index, parseErr := strconv.Atoi(c.Param("index"))
	if parseErr != nil || index < 0 || index >= count {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found", "code": "not_found"})
		return
	}
	data, err := os.ReadFile(filepath.Join(visualsDir(h.config.UploadDir, job.ID), folder, fmt.Sprintf("%d.jpg", index)))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found", "code": "not_found"})
		return
	}
	c.Header("Cache-Control", visualsCacheControl)
	c.Data(http.StatusOK, "image/jpeg", data)
}

// GetJobFrame serves one extracted frame.
// @Summary Get a job's video frame
// @Tags transcription
// @Produce image/jpeg
// @Router /api/v1/transcription/{id}/frames/{index} [get]
// @Security ApiKeyAuth
func (h *Handler) GetJobFrame(c *gin.Context) { h.serveVisualImage(c, "frames") }

// GetJobSheet serves one contact sheet.
// @Summary Get a job's contact sheet
// @Tags transcription
// @Produce image/jpeg
// @Router /api/v1/transcription/{id}/sheets/{index} [get]
// @Security ApiKeyAuth
func (h *Handler) GetJobSheet(c *gin.Context) { h.serveVisualImage(c, "sheets") }

// GetJobVideo streams the video kept with a job; Range requests work.
// @Summary Get a job's video
// @Tags transcription
// @Router /api/v1/transcription/{id}/video [get]
// @Security ApiKeyAuth
func (h *Handler) GetJobVideo(c *gin.Context) {
	job, err := h.jobRepo.FindByID(c.Request.Context(), c.Param("id"))
	if err != nil || job.VideoPath == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Video not found", "code": "not_found"})
		return
	}
	file, err := os.Open(*job.VideoPath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Video not found", "code": "not_found"})
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Video not found", "code": "not_found"})
		return
	}
	c.Header("Content-Type", videoContentType(*job.VideoPath))
	http.ServeContent(c.Writer, c.Request, filepath.Base(*job.VideoPath), info.ModTime(), file)
}

// BuildJobVisuals builds (or rebuilds) a link job's frames and sheets, from
// the kept video or, failing that, a fresh download. The job's audio,
// transcript and status are never touched.
// @Summary Build a job's visuals
// @Tags transcription
// @Produce json
// @Router /api/v1/transcription/{id}/visuals [post]
// @Security ApiKeyAuth
// @Security BearerAuth
func (h *Handler) BuildJobVisuals(c *gin.Context) {
	job, ok := h.findVisualsJob(c)
	if !ok {
		return
	}
	source := ""
	supported := false
	if job.SourceURL != nil {
		source, supported = linkSource(*job.SourceURL)
	}
	if !supported {
		c.JSON(http.StatusBadRequest, gin.H{"error": "This recording has no source link to fetch video from.", "code": "no_source_link"})
		return
	}
	if job.VisualsStatus == models.VisualsPending {
		c.JSON(http.StatusConflict, gin.H{"error": "The frames are already being built.", "code": "visuals_pending"})
		return
	}

	ctx := context.WithoutCancel(c.Request.Context())
	job.VisualsStatus = models.VisualsPending
	job.VisualsError = nil
	job.Frames = nil
	job.Sheets = nil
	if err := h.saveVisuals(ctx, job); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start the build.", "code": "internal_error"})
		return
	}
	if job.LinkKind != nil && *job.LinkKind == linkKindPost {
		go h.rebuildPostVisuals(ctx, job)
	} else {
		go h.rebuildVisuals(ctx, job, source)
	}
	c.JSON(http.StatusAccepted, h.visualsBody(job))
}

// rebuildVisuals uses the kept video when it is still on disk, else
// downloads the video again without extracting audio.
func (h *Handler) rebuildVisuals(ctx context.Context, job *models.TranscriptionJob, source string) {
	if job.VideoPath != nil {
		if _, err := os.Stat(*job.VideoPath); err == nil {
			h.runVisualsBuild(ctx, job.ID, *job.VideoPath, false)
			return
		}
	}

	uploadDir := h.config.UploadDir
	template := filepath.Join(uploadDir, job.ID+".video.%(ext)s")
	command := exec.CommandContext(ctx, "yt-dlp", withLinkProxy(h.config.LinkImportProxy, source,
		"-f", linkVideoFormat,
		"--merge-output-format", "mp4",
		"--no-playlist",
		"-o", template,
		*job.SourceURL,
	)...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	runErr := command.Run()

	var video string
	leftovers, _ := filepath.Glob(filepath.Join(uploadDir, job.ID+".video.*"))
	for _, path := range leftovers {
		if runErr == nil && strings.EqualFold(filepath.Ext(path), ".mp4") {
			video = path
		}
	}
	if video == "" && runErr == nil {
		for _, path := range leftovers {
			if ext := strings.ToLower(filepath.Ext(path)); ext != ".mp3" && ext != ".part" && ext != ".ytdl" {
				video = path
				break
			}
		}
	}
	for _, path := range leftovers {
		if path != video {
			_ = os.Remove(path)
		}
	}

	if runErr != nil || video == "" {
		logger.Warn("Video download for visuals failed", "job_id", job.ID, "stderr", stderr.String())
		failure := classifyLinkFailure(source, stderr.String())
		reason := "The video download failed. " + failure.message
		fresh, err := h.jobRepo.FindByID(ctx, job.ID)
		if err != nil {
			return
		}
		fresh.VisualsStatus = models.VisualsFailed
		fresh.VisualsError = &reason
		_ = h.saveVisuals(ctx, fresh)
		return
	}
	h.runVisualsBuild(ctx, job.ID, video, true)
}
