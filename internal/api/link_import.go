package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"scriberr/internal/models"
	"scriberr/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	sourceInstagram = "instagram"
	sourceTikTok    = "tiktok"

	unsupportedLinkMessage = "That link is not supported. Paste a YouTube, Instagram reel or post, or TikTok video link."
	noAudioTrackMessage    = "The post has no audio track; its caption was saved."
	noAudioStderrMarker    = "unable to obtain file audio codec"
	maxCaptionTitleRunes   = 80
)

var (
	instagramPathPattern      = regexp.MustCompile(`^/(reel|reels|p)/[A-Za-z0-9_-]+/?$`)
	tiktokVideoPathPattern    = regexp.MustCompile(`^/@[^/]+/video/[0-9]+/?$`)
	tiktokShortLinkPathRegexp = regexp.MustCompile(`^/[A-Za-z0-9_-]+/?$`)
)

// linkSource names the source a link belongs to (youtube, instagram or
// tiktok) and reports whether the link is one the import accepts.
func linkSource(rawURL string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", false
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "youtube.com", "www.youtube.com", "m.youtube.com", "music.youtube.com", "youtu.be":
		return sourceYouTube, true
	case "instagram.com", "www.instagram.com":
		return sourceInstagram, instagramPathPattern.MatchString(parsed.Path)
	case "tiktok.com", "www.tiktok.com", "m.tiktok.com":
		return sourceTikTok, tiktokVideoPathPattern.MatchString(parsed.Path)
	case "vm.tiktok.com", "vt.tiktok.com":
		return sourceTikTok, tiktokShortLinkPathRegexp.MatchString(parsed.Path)
	}
	return "", false
}

func sourceDisplayName(source string) string {
	switch source {
	case sourceInstagram:
		return "Instagram"
	case sourceTikTok:
		return "TikTok"
	}
	return "YouTube"
}

// linkMetadata is the part of yt-dlp's info JSON the app uses.
type linkMetadata struct {
	ID              string
	Title           string
	Description     string
	Channel         *string
	DurationSeconds *int
	Language        *string
	ThumbnailURL    string
}

// withLinkProxy prepends yt-dlp's --proxy flag when a link-import proxy is set
// and the link's source is one the proxy serves. The egress proxy forwards
// only Instagram and TikTok hosts, so YouTube always goes direct.
func withLinkProxy(proxy, source string, args ...string) []string {
	if proxy == "" || (source != sourceInstagram && source != sourceTikTok) {
		return args
	}
	return append([]string{"--proxy", proxy}, args...)
}

// fetchLinkMetadata asks yt-dlp for a post's info without downloading it. On
// failure it returns yt-dlp's stderr for classification.
func fetchLinkMetadata(link, source, proxy string) (*linkMetadata, string, error) {
	cmd := exec.Command("yt-dlp", withLinkProxy(proxy, source, "--dump-json", "--skip-download", "--no-playlist", link)...)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, stderr.String(), err
	}
	var raw struct {
		ID          *string  `json:"id"`
		Title       *string  `json:"title"`
		Description *string  `json:"description"`
		Channel     *string  `json:"channel"`
		Uploader    *string  `json:"uploader"`
		Duration    *float64 `json:"duration"`
		Language    *string  `json:"language"`
		Thumbnail   *string  `json:"thumbnail"`
	}
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
		return nil, stderr.String(), fmt.Errorf("unreadable yt-dlp metadata: %w", err)
	}
	present := func(value *string) string {
		if value == nil {
			return ""
		}
		trimmed := strings.TrimSpace(*value)
		if trimmed == "NA" {
			return ""
		}
		return trimmed
	}
	optional := func(value *string) *string {
		if text := present(value); text != "" {
			return &text
		}
		return nil
	}
	metadata := &linkMetadata{
		ID:           present(raw.ID),
		Title:        present(raw.Title),
		Language:     optional(raw.Language),
		ThumbnailURL: present(raw.Thumbnail),
	}
	if raw.Duration != nil {
		seconds := int(*raw.Duration)
		metadata.DurationSeconds = &seconds
	}

	switch source {
	case sourceInstagram:
		metadata.Channel = authorHandle(present(raw.Channel), present(raw.Uploader))
		metadata.Description = present(raw.Description)
		if line := captionTitle(metadata.Description); line != "" {
			metadata.Title = line
		}
	case sourceTikTok:
		metadata.Channel = authorHandle(present(raw.Uploader), present(raw.Channel))
		metadata.Description = present(raw.Description)
	default:
		metadata.Channel = optional(raw.Channel)
	}
	return metadata, stderr.String(), nil
}

// authorHandle returns the first non-empty candidate as an @handle.
func authorHandle(candidates ...string) *string {
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		handle := "@" + strings.TrimPrefix(candidate, "@")
		return &handle
	}
	return nil
}

// captionTitle is the first non-empty line of a caption, cut at 80 runes.
func captionTitle(caption string) string {
	for _, line := range strings.Split(caption, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		runes := []rune(line)
		if len(runes) > maxCaptionTitleRunes {
			return string(runes[:maxCaptionTitleRunes]) + "…"
		}
		return line
	}
	return ""
}

// linkFailure is a refused import: an HTTP status, a stable code and a
// human message.
type linkFailure struct {
	status  int
	code    string
	message string
}

func (failure *linkFailure) respond(c *gin.Context) {
	c.JSON(failure.status, gin.H{"error": failure.message, "code": failure.code})
}

// classifyLinkFailure maps yt-dlp stderr to a failure for the link's source.
func classifyLinkFailure(source, stderr string) linkFailure {
	lowered := strings.ToLower(stderr)
	containsAny := func(markers ...string) bool {
		for _, marker := range markers {
			if strings.Contains(lowered, marker) {
				return true
			}
		}
		return false
	}
	switch source {
	case sourceInstagram, sourceTikTok:
		name := sourceDisplayName(source)
		if source == sourceInstagram && containsAny("login required", "--cookies") {
			return linkFailure{http.StatusBadGateway, "instagram_login_required", "Instagram asked for a login before showing this post. Try again later."}
		}
		if containsAny("429", "too many requests") {
			return linkFailure{http.StatusBadGateway, "rate_limited", name + " is limiting requests. Try again later."}
		}
		unavailableMarkers := []string{"private", "unavailable", "not found"}
		if source == sourceInstagram {
			unavailableMarkers = append(unavailableMarkers, "404")
		}
		if containsAny(unavailableMarkers...) {
			return linkFailure{http.StatusUnprocessableEntity, "video_unavailable", "This post is unavailable: it may be private or removed."}
		}
		return linkFailure{http.StatusBadGateway, "download_failed", "The " + name + " download failed. Check the link and try again."}
	}
	if containsAny("private", "unavailable", "removed", "members-only", "members only", "age-restricted", "confirm your age") {
		return linkFailure{http.StatusUnprocessableEntity, "video_unavailable", "This video is unavailable: it may be private, removed, members-only or age-restricted."}
	}
	if containsAny("sign in to confirm", "not a bot", "http error 403", "http error 429", "po token") {
		return linkFailure{http.StatusBadGateway, "youtube_blocked", "YouTube blocked the download request. Try again later."}
	}
	return linkFailure{http.StatusBadGateway, "download_failed", "The YouTube download failed. Check the link and try again."}
}

// suggestProfileWithFallback prefers the profile for the post's language,
// then English, then Spanish.
func suggestProfileWithFallback(profiles []models.TranscriptionProfile, language *string) *string {
	if language != nil {
		if suggestion := suggestProfileForLanguage(profiles, *language); suggestion != nil {
			return suggestion
		}
	}
	if suggestion := suggestProfileForLanguage(profiles, "en"); suggestion != nil {
		return suggestion
	}
	return suggestProfileForLanguage(profiles, "es")
}

// linkImportRequest is one import. Profile is nil when the profile is to be
// chosen from the post's language.
type linkImportRequest struct {
	URL        string
	Source     string
	Title      *string
	ExternalID string
	Profile    *models.TranscriptionProfile
}

// importLink downloads a post's audio and stores it as a job. A post whose
// download has no audio track is stored as a failed job that keeps its
// caption.
func (h *Handler) importLink(ctx context.Context, request linkImportRequest) (*models.TranscriptionJob, *linkFailure) {
	uploadDir := h.config.UploadDir
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		return nil, &linkFailure{http.StatusInternalServerError, "download_failed", "Failed to create upload directory"}
	}

	jobID := uuid.New().String()
	filePath := filepath.Join(uploadDir, fmt.Sprintf("%s.%%(ext)s", jobID))

	// Remove any partial download for this job on every failure path.
	removePartials := func() {
		partials, _ := filepath.Glob(filepath.Join(uploadDir, jobID+".*"))
		for _, partial := range partials {
			_ = os.Remove(partial)
		}
	}

	title := sourceDisplayName(request.Source) + " Audio"
	var metadata *linkMetadata
	fetched, metadataStderr, metadataErr := fetchLinkMetadata(request.URL, request.Source, h.config.LinkImportProxy)
	if metadataErr != nil {
		logger.Warn("Failed to get link metadata", "url", request.URL, "error", metadataErr.Error(), "stderr", metadataStderr)
		failure := classifyLinkFailure(request.Source, metadataStderr)
		if failure.code != "download_failed" {
			return nil, &failure
		}
	} else {
		metadata = fetched
		if metadata.Title != "" {
			title = metadata.Title
		}
	}
	if request.Title != nil && *request.Title != "" {
		title = *request.Title
	}

	profile := request.Profile
	if profile == nil {
		var language *string
		if metadata != nil {
			language = metadata.Language
		}
		profiles, _, _ := h.profileRepo.List(ctx, 0, 1000)
		if suggestedID := suggestProfileWithFallback(profiles, language); suggestedID != nil {
			profile, _ = h.profileRepo.FindByID(ctx, *suggestedID)
		}
		if profile == nil {
			return nil, &linkFailure{http.StatusInternalServerError, "no_profile", "No transcription profile matches this post. Create a Spanish or English profile."}
		}
	}

	logger.Info("Starting link download", "source", request.Source, "url", request.URL, "job_id", jobID)
	downloadStart := time.Now()
	ytDlpCmd := exec.Command("yt-dlp", withLinkProxy(h.config.LinkImportProxy, request.Source,
		"--extract-audio",
		"--audio-format", "mp3",
		"--audio-quality", "0", // best quality
		"--output", filePath,
		"--no-playlist",
		request.URL,
	)...)
	var stderr bytes.Buffer
	ytDlpCmd.Stderr = &stderr

	noAudio := false
	if err := ytDlpCmd.Run(); err != nil {
		stderrOutput := stderr.String()
		logger.Error("Link download failed",
			"url", request.URL,
			"job_id", jobID,
			"error", err.Error(),
			"stderr", stderrOutput,
			"duration", time.Since(downloadStart))

		noAudio = strings.Contains(strings.ToLower(stderrOutput), noAudioStderrMarker)
		if !noAudio {
			removePartials()
			failure := classifyLinkFailure(request.Source, stderrOutput)
			return nil, &failure
		}
	}

	// Find the actual downloaded file (yt-dlp changes the extension)
	matches, err := filepath.Glob(filepath.Join(uploadDir, jobID+".*"))
	if err != nil || len(matches) == 0 {
		removePartials()
		return nil, &linkFailure{http.StatusInternalServerError, "download_failed", "Downloaded file not found"}
	}
	actualFilePath := matches[0]
	if fileInfo, statErr := os.Stat(actualFilePath); statErr == nil {
		logger.Info("Link download completed",
			"url", request.URL,
			"job_id", jobID,
			"file_path", actualFilePath,
			"file_size_mb", fmt.Sprintf("%.2f", float64(fileInfo.Size())/1024/1024),
			"duration", time.Since(downloadStart))
	}

	profileName := profile.Name
	sourceURL := request.URL
	job := models.TranscriptionJob{
		ID:          jobID,
		AudioPath:   actualFilePath,
		Status:      models.StatusPending,
		Source:      request.Source,
		SourceURL:   &sourceURL,
		Title:       &title,
		ProfileName: &profileName,
		Parameters:  profile.Parameters,
		Execution:   profileExecutionSnapshot(profile),
		Diarization: profile.Parameters.Diarize,
	}
	if request.ExternalID != "" {
		externalID := request.ExternalID
		job.ExternalID = &externalID
	}
	videoID := ""
	if metadata != nil {
		job.SourceChannel = metadata.Channel
		videoID = metadata.ID
		// YouTube descriptions are long promo text; only the short captions
		// of Instagram and TikTok posts are worth keeping.
		if request.Source != sourceYouTube && metadata.Description != "" {
			caption := metadata.Description
			job.SourceDescription = &caption
		}
	}
	if request.Source == sourceYouTube {
		job.ThumbnailPath = h.saveYouTubeThumbnail(ctx, uploadDir, jobID, videoID)
	}
	if noAudio {
		reason := noAudioTrackMessage
		job.Status = models.StatusFailed
		job.ErrorMessage = &reason
	}

	if err := h.jobRepo.Create(ctx, &job); err != nil {
		removePartials()
		if job.ThumbnailPath != nil {
			_ = os.Remove(*job.ThumbnailPath)
		}
		return nil, &linkFailure{http.StatusInternalServerError, "download_failed", "Failed to save transcription record"}
	}

	if !noAudio {
		if err := h.taskQueue.EnqueueJob(jobID); err != nil {
			logger.Warn("Failed to queue link job", "job_id", jobID, "error", err.Error())
			reason := fmt.Sprintf("Downloaded, but could not be queued: %v", err)
			job.Status = models.StatusUploaded
			job.ErrorMessage = &reason
			_ = h.jobRepo.Update(ctx, &job)
		}
	}

	applySourceDescription(&job)
	return &job, nil
}

// DownloadFromYouTube is the import dialog's endpoint: any supported link,
// with the profile the person picked.
// @Summary Import audio from a YouTube, Instagram or TikTok link
// @Description Download the audio of a supported link and queue it with the chosen profile
// @Tags transcription
// @Accept json
// @Produce json
// @Param request body YouTubeDownloadRequest true "Link import request"
// @Success 200 {object} models.TranscriptionJob
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/v1/transcription/youtube [post]
// @Security ApiKeyAuth
// @Security BearerAuth
func (h *Handler) DownloadFromYouTube(c *gin.Context) {
	var req YouTubeDownloadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "invalid_url"})
		return
	}

	// Nothing below depends on the browser still waiting: a finished download
	// must not be thrown away when the tab closes.
	ctx := context.WithoutCancel(c.Request.Context())

	req.URL = strings.TrimSpace(req.URL)
	source, supported := linkSource(req.URL)
	if !supported {
		c.JSON(http.StatusBadRequest, gin.H{"error": unsupportedLinkMessage, "code": "invalid_url"})
		return
	}
	if strings.TrimSpace(req.ProfileID) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Choose a transcription profile for this link.", "code": "profile_required"})
		return
	}
	profile, err := h.profileRepo.FindByID(ctx, req.ProfileID)
	if err != nil || profile == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "That transcription profile does not exist.", "code": "unknown_profile"})
		return
	}

	job, failure := h.importLink(ctx, linkImportRequest{URL: req.URL, Source: source, Title: req.Title, Profile: profile})
	if failure != nil {
		failure.respond(c)
		return
	}
	c.JSON(http.StatusOK, job)
}

// PreviewYouTube looks a link up without downloading it and suggests the
// profile whose language matches the post's.
// @Summary Preview a YouTube, Instagram or TikTok link
// @Tags transcription
// @Accept json
// @Produce json
// @Router /api/v1/transcription/youtube/preview [post]
// @Security ApiKeyAuth
// @Security BearerAuth
func (h *Handler) PreviewYouTube(c *gin.Context) {
	var req struct {
		URL string `json:"url" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "invalid_url"})
		return
	}
	link := strings.TrimSpace(req.URL)
	source, supported := linkSource(link)
	if !supported {
		c.JSON(http.StatusBadRequest, gin.H{"error": unsupportedLinkMessage, "code": "invalid_url"})
		return
	}

	metadata, stderr, err := fetchLinkMetadata(link, source, h.config.LinkImportProxy)
	if err != nil {
		logger.Warn("Link preview failed", "url", link, "error", err.Error(), "stderr", stderr)
		failure := classifyLinkFailure(source, stderr)
		failure.respond(c)
		return
	}

	var suggestedProfileID *string
	if source != sourceYouTube || metadata.Language != nil {
		profiles, _, _ := h.profileRepo.List(c.Request.Context(), 0, 1000)
		if source == sourceYouTube {
			suggestedProfileID = suggestProfileForLanguage(profiles, *metadata.Language)
		} else {
			suggestedProfileID = suggestProfileWithFallback(profiles, metadata.Language)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"video_id":             metadata.ID,
		"title":                metadata.Title,
		"channel":              metadata.Channel,
		"duration_seconds":     metadata.DurationSeconds,
		"language":             metadata.Language,
		"thumbnail_url":        metadata.ThumbnailURL,
		"suggested_profile_id": suggestedProfileID,
	})
}

// linkImportResponse is what an automated caller sees of an imported link.
type linkImportResponse struct {
	JobID         string  `json:"job_id"`
	Status        string  `json:"status"`
	Title         string  `json:"title"`
	Source        string  `json:"source"`
	ProfileName   string  `json:"profile_name"`
	RecordingPath string  `json:"recording_path"`
	Error         *string `json:"error"`
}

func newLinkImportResponse(job *models.TranscriptionJob) linkImportResponse {
	body := linkImportResponse{
		JobID:         job.ID,
		Status:        string(job.Status),
		Source:        job.Source,
		RecordingPath: "/audio/" + job.ID,
		Error:         job.ErrorMessage,
	}
	if job.Title != nil {
		body.Title = *job.Title
	}
	if job.ProfileName != nil {
		body.ProfileName = *job.ProfileName
	}
	return body
}

// externalIDFinder is implemented by job repositories that can look a job up
// by the id its caller gave it.
type externalIDFinder interface {
	FindByExternalID(ctx context.Context, externalID string) (*models.TranscriptionJob, error)
}

// ImportLink imports a link for an automated caller. Repeating an
// external_id returns the job it created instead of downloading again.
// @Summary Import a link for an automated caller
// @Tags transcription
// @Accept json
// @Produce json
// @Router /api/v1/transcription/links [post]
// @Security ApiKeyAuth
// @Security BearerAuth
func (h *Handler) ImportLink(c *gin.Context) {
	var req struct {
		URL        string `json:"url" binding:"required"`
		ExternalID string `json:"external_id"`
		ProfileID  string `json:"profile_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.URL) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Send a JSON body with a url.", "code": "invalid_request"})
		return
	}
	ctx := context.WithoutCancel(c.Request.Context())

	link := strings.TrimSpace(req.URL)
	source, supported := linkSource(link)
	if !supported {
		c.JSON(http.StatusBadRequest, gin.H{"error": unsupportedLinkMessage, "code": "unsupported_url"})
		return
	}

	externalID := strings.TrimSpace(req.ExternalID)
	if finder, ok := h.jobRepo.(externalIDFinder); ok && externalID != "" {
		existing, err := finder.FindByExternalID(ctx, externalID)
		if err == nil {
			c.JSON(http.StatusOK, newLinkImportResponse(existing))
			return
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to look up the external id.", "code": "internal_error"})
			return
		}
	}

	var profile *models.TranscriptionProfile
	if profileID := strings.TrimSpace(req.ProfileID); profileID != "" {
		found, err := h.profileRepo.FindByID(ctx, profileID)
		if err != nil || found == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "That transcription profile does not exist.", "code": "unknown_profile"})
			return
		}
		profile = found
	}

	job, failure := h.importLink(ctx, linkImportRequest{URL: link, Source: source, ExternalID: externalID, Profile: profile})
	if failure != nil {
		failure.respond(c)
		return
	}
	c.JSON(http.StatusCreated, newLinkImportResponse(job))
}

// GetLinkImport reports where an imported link's job stands.
// @Summary Get the state of an imported link
// @Tags transcription
// @Produce json
// @Router /api/v1/transcription/links/{id} [get]
// @Security ApiKeyAuth
// @Security BearerAuth
func (h *Handler) GetLinkImport(c *gin.Context) {
	job, err := h.jobRepo.FindByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Import not found.", "code": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read the import.", "code": "internal_error"})
		return
	}
	c.JSON(http.StatusOK, newLinkImportResponse(job))
}
