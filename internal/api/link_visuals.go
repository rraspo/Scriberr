package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"scriberr/internal/models"
	"scriberr/pkg/logger"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	// linkVideoFormat asks yt-dlp for the best video up to 720p with audio.
	linkVideoFormat = "bv*[height<=720]+ba/b[height<=720]/bv*+ba/b"

	frameScaleFilter    = "scale='if(gt(iw,ih),720,-2)':'if(gt(iw,ih),-2,720)'"
	sceneChangeCutoff   = "0.3"
	longVideoSeconds    = 600
	minSceneGapSeconds  = 0.5
	interruptedVisuals  = "Interrupted by a restart; POST /api/v1/transcription/%s/visuals to rebuild."
	visualsCacheControl = "private, max-age=31536000, immutable"
)

// maxKeptVideoBytes is the largest video kept after its frames are built.
var maxKeptVideoBytes int64 = 500 << 20

var sceneTimePattern = regexp.MustCompile(`pts_time:([0-9]+(?:\.[0-9]+)?)`)

// jobVisualsUpdater is implemented by job repositories that can write only a
// job's visuals columns.
type jobVisualsUpdater interface {
	UpdateVisuals(ctx context.Context, job *models.TranscriptionJob) error
}

func (h *Handler) saveVisuals(ctx context.Context, job *models.TranscriptionJob) error {
	if updater, ok := h.jobRepo.(jobVisualsUpdater); ok {
		return updater.UpdateVisuals(ctx, job)
	}
	return h.jobRepo.Update(ctx, job)
}

// frameRateFor is the sampling rate: two frames a second under 20 s, one a
// second up to ten minutes, then about 600 frames spread over the video.
func frameRateFor(durationSeconds float64) float64 {
	switch {
	case durationSeconds < 20:
		return 2
	case durationSeconds <= longVideoSeconds:
		return 1
	}
	return longVideoSeconds / durationSeconds
}

// frameTimes lists the frame timestamps: an even grid from zero, plus, for
// videos over ten minutes, every scene change not within half a second of a
// time already kept.
func frameTimes(durationSeconds float64, scenes []float64) []float64 {
	rate := frameRateFor(durationSeconds)
	count := int(math.Ceil(durationSeconds*rate - 1e-9))
	times := make([]float64, 0, count+len(scenes))
	for step := 0; step < count; step++ {
		times = append(times, float64(step)/rate)
	}
	if durationSeconds > longVideoSeconds {
		for _, scene := range scenes {
			if scene < 0 || scene >= durationSeconds {
				continue
			}
			near := false
			for _, kept := range times {
				if math.Abs(kept-scene) < minSceneGapSeconds {
					near = true
					break
				}
			}
			if !near {
				times = append(times, scene)
			}
		}
	}
	sort.Float64s(times)
	return times
}

func visualsDir(uploadDir, jobID string) string {
	return filepath.Join(uploadDir, "visuals", jobID)
}

func videoContentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	}
	return "application/octet-stream"
}

func probeNumbers(ctx context.Context, args ...string) ([]float64, error) {
	output, err := exec.CommandContext(ctx, "ffprobe", args...).Output()
	if err != nil {
		return nil, err
	}
	var numbers []float64
	for _, field := range strings.Split(strings.TrimSpace(string(output)), ",") {
		number, err := strconv.ParseFloat(strings.TrimSpace(field), 64)
		if err != nil {
			return nil, fmt.Errorf("unreadable ffprobe output")
		}
		numbers = append(numbers, number)
	}
	return numbers, nil
}

// extractedVisuals is what a build leaves on disk and in the job row.
type extractedVisuals struct {
	frames   []models.JobFrame
	sheets   []models.JobSheet
	duration float64
}

// extractVisuals probes the video, extracts its frames into
// <visuals>/<job>/frames and tiles them into sheets. It removes everything
// it wrote when it fails.
func (h *Handler) extractVisuals(ctx context.Context, jobID, videoPath string) (result *extractedVisuals, err error) {
	root := visualsDir(h.config.UploadDir, jobID)
	defer func() {
		if err != nil {
			_ = os.RemoveAll(root)
		}
	}()
	_ = os.RemoveAll(root)
	frameDir := filepath.Join(root, "frames")
	rawDir := filepath.Join(root, "raw")
	for _, dir := range []string{frameDir, rawDir} {
		if err = os.MkdirAll(dir, 0o755); err != nil {
			return nil, errors.New("Could not create the frames folder.")
		}
	}

	durations, probeErr := probeNumbers(ctx, "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", videoPath)
	if probeErr != nil || len(durations) != 1 || durations[0] <= 0 {
		return nil, errors.New("Could not read the video's duration.")
	}
	duration := durations[0]
	if _, probeErr = probeNumbers(ctx, "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height", "-of", "csv=p=0", videoPath); probeErr != nil {
		return nil, errors.New("The download has no video stream.")
	}

	rate := frameRateFor(duration)
	var scenes []float64
	if duration > longVideoSeconds {
		scenes = detectScenes(ctx, videoPath)
	}
	times := frameTimes(duration, scenes)
	uniform := frameTimes(duration, nil)
	uniformPosition := make(map[float64]int, len(uniform))
	for position, seconds := range uniform {
		uniformPosition[seconds] = position
	}

	grid := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", videoPath,
		"-vf", fmt.Sprintf("fps=%s,%s", strconv.FormatFloat(rate, 'f', -1, 64), frameScaleFilter),
		"-q:v", "4", filepath.Join(rawDir, "%05d.jpg"))
	if output, runErr := grid.CombinedOutput(); runErr != nil {
		logger.Warn("Frame extraction failed", "job_id", jobID, "error", runErr.Error(), "output", string(output))
		return nil, errors.New("Frame extraction failed.")
	}

	// ffmpeg's fps filter can emit fewer frames than the container duration
	// predicts, so grid positions past the produced count are dropped.
	producedFiles, _ := filepath.Glob(filepath.Join(rawDir, "*.jpg"))
	produced := len(producedFiles)
	if produced == 0 {
		return nil, errors.New("Frame extraction produced no frames.")
	}
	if produced < len(uniform) {
		logger.Warn("Frame grid came up short", "job_id", jobID, "expected", len(uniform), "produced", produced)
		kept := make([]float64, 0, len(times))
		for _, seconds := range times {
			if position, onGrid := uniformPosition[seconds]; onGrid && position >= produced {
				continue
			}
			kept = append(kept, seconds)
		}
		times = kept
	}

	frames := make([]models.JobFrame, 0, len(times))
	for index, seconds := range times {
		target := filepath.Join(frameDir, fmt.Sprintf("%d.jpg", index))
		if position, onGrid := uniformPosition[seconds]; onGrid {
			source := filepath.Join(rawDir, fmt.Sprintf("%05d.jpg", position+1))
			if err = os.Rename(source, target); err != nil {
				return nil, errors.New("Frame extraction produced fewer frames than expected.")
			}
		} else {
			single := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-ss", strconv.FormatFloat(seconds, 'f', 3, 64),
				"-i", videoPath, "-frames:v", "1", "-vf", frameScaleFilter, "-q:v", "4", target)
			if output, runErr := single.CombinedOutput(); runErr != nil {
				logger.Warn("Scene frame extraction failed", "job_id", jobID, "error", runErr.Error(), "output", string(output))
				return nil, errors.New("Frame extraction failed.")
			}
		}
		frames = append(frames, models.JobFrame{
			Index:            index,
			TimestampSeconds: seconds,
			URL:              fmt.Sprintf("/api/v1/transcription/%s/frames/%d", jobID, index),
		})
	}
	_ = os.RemoveAll(rawDir)

	sheets, sheetErr := buildContactSheets(frameDir, filepath.Join(root, "sheets"), frames, jobID)
	if sheetErr != nil {
		logger.Warn("Contact sheet build failed", "job_id", jobID, "error", sheetErr.Error())
		return nil, errors.New("Could not build the contact sheets.")
	}
	return &extractedVisuals{frames: frames, sheets: sheets, duration: duration}, nil
}

// detectScenes lists the scene-change times of a long video. A failure only
// means the even grid stands alone.
func detectScenes(ctx context.Context, videoPath string) []float64 {
	command := exec.CommandContext(ctx, "ffmpeg", "-v", "info", "-skip_frame", "nokey", "-i", videoPath,
		"-vf", fmt.Sprintf("select='gt(scene,%s)',showinfo", sceneChangeCutoff), "-f", "null", "-")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil
	}
	var scenes []float64
	for _, match := range sceneTimePattern.FindAllStringSubmatch(stderr.String(), -1) {
		if seconds, err := strconv.ParseFloat(match[1], 64); err == nil {
			scenes = append(scenes, seconds)
		}
	}
	return scenes
}

// startVisualsBuild marks the job pending (the caller has saved that) and
// builds in the background. A downloaded video is the temp copy of a backfill.
func (h *Handler) startVisualsBuild(ctx context.Context, jobID, videoPath string, downloaded bool) {
	go h.runVisualsBuild(context.WithoutCancel(ctx), jobID, videoPath, downloaded)
}

func (h *Handler) runVisualsBuild(ctx context.Context, jobID, videoPath string, downloaded bool) {
	result, buildErr := h.extractVisuals(ctx, jobID, videoPath)
	job, err := h.jobRepo.FindByID(ctx, jobID)
	if err != nil {
		// The job was deleted while its frames were building.
		_ = os.RemoveAll(visualsDir(h.config.UploadDir, jobID))
		if downloaded {
			_ = os.Remove(videoPath)
		}
		return
	}

	if buildErr != nil {
		reason := buildErr.Error()
		job.VisualsStatus = models.VisualsFailed
		job.VisualsError = &reason
		job.Frames = nil
		job.Sheets = nil
		if downloaded {
			_ = os.Remove(videoPath)
		}
		if err := h.saveVisuals(ctx, job); err != nil {
			logger.Warn("Could not save the visuals failure", "job_id", jobID, "error", err.Error())
		}
		return
	}

	keptPath := videoPath
	info, statErr := os.Stat(videoPath)
	switch {
	case statErr != nil:
		keptPath = ""
	case info.Size() > maxKeptVideoBytes && videoPath != job.AudioPath:
		_ = os.Remove(videoPath)
		keptPath = ""
	case downloaded:
		final := filepath.Join(h.config.UploadDir, jobID+filepath.Ext(videoPath))
		if final != job.AudioPath && os.Rename(videoPath, final) == nil {
			keptPath = final
		}
	}

	job.VisualsStatus = models.VisualsReady
	job.VisualsError = nil
	job.Frames = result.frames
	job.Sheets = result.sheets
	duration := result.duration
	job.VideoDurationSeconds = &duration
	if keptPath == "" {
		job.VideoPath = nil
	} else {
		job.VideoPath = &keptPath
	}
	if err := h.saveVisuals(ctx, job); err != nil {
		logger.Warn("Could not save the visuals", "job_id", jobID, "error", err.Error())
	}
}

// FailInterruptedVisuals marks every build a restart cut short as failed,
// with the way to retry it.
func FailInterruptedVisuals(db *gorm.DB) error {
	var jobs []models.TranscriptionJob
	if err := db.Select("id").Where("visuals_status = ?", models.VisualsPending).Find(&jobs).Error; err != nil {
		return err
	}
	for _, job := range jobs {
		reason := fmt.Sprintf(interruptedVisuals, job.ID)
		if err := db.Model(&models.TranscriptionJob{}).Where("id = ?", job.ID).
			Updates(map[string]interface{}{"visuals_status": models.VisualsFailed, "visuals_error": reason}).Error; err != nil {
			return err
		}
	}
	logger.Info("Marked interrupted visuals builds as failed", "count", len(jobs))
	return nil
}

// downloadedFiles splits a link download's files into the extracted audio
// (<job>.mp3) and the kept video, preferring .mp4.
func downloadedFiles(uploadDir, jobID string) (audio, video string) {
	matches, _ := filepath.Glob(filepath.Join(uploadDir, jobID+".*"))
	for _, match := range matches {
		extension := strings.ToLower(filepath.Ext(match))
		switch extension {
		case ".mp3":
			audio = match
		case ".part", ".ytdl", ".temp", ".json", ".jpg", ".webp":
		case ".mp4":
			video = match
		default:
			if video == "" || strings.ToLower(filepath.Ext(video)) != ".mp4" {
				video = match
			}
		}
	}
	return audio, video
}

func (h *Handler) visualsBody(job *models.TranscriptionJob) gin.H {
	status := job.VisualsStatus
	if status == "" {
		status = "none"
	}
	frames := job.Frames
	if frames == nil {
		frames = []models.JobFrame{}
	}
	sheets := job.Sheets
	if sheets == nil {
		sheets = []models.JobSheet{}
	}
	var video *models.JobVideo
	if job.VideoPath != nil {
		if info, err := os.Stat(*job.VideoPath); err == nil {
			video = &models.JobVideo{
				URL:         "/api/v1/transcription/" + job.ID + "/video",
				ContentType: videoContentType(*job.VideoPath),
				SizeBytes:   info.Size(),
			}
			if job.VideoDurationSeconds != nil {
				video.DurationSeconds = *job.VideoDurationSeconds
			}
		}
	}
	return gin.H{"status": status, "error": job.VisualsError, "sheets": sheets, "frames": frames, "video": video}
}

func (h *Handler) findVisualsJob(c *gin.Context) (*models.TranscriptionJob, bool) {
	job, err := h.jobRepo.FindByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Recording not found.", "code": "not_found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read the recording.", "code": "internal_error"})
		}
		return nil, false
	}
	return job, true
}

// GetJobVisuals reports a job's frames, contact sheets and kept video.
// @Summary Get a job's visuals
// @Tags transcription
// @Produce json
// @Router /api/v1/transcription/{id}/visuals [get]
// @Security ApiKeyAuth
// @Security BearerAuth
func (h *Handler) GetJobVisuals(c *gin.Context) {
	job, ok := h.findVisualsJob(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, h.visualsBody(job))
}
