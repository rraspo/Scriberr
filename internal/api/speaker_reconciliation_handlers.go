package api

import (
	"errors"
	"fmt"
	"net/http"

	"scriberr/internal/transcription"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// SetJobReconciler wires the on-demand single-job speaker reconciler into
// the handler, following the same post-startup setter pattern as
// SetSpeakerProfileRepo and SetSpeakerEmbeddingExtractor.
func (h *Handler) SetJobReconciler(reconciler *transcription.JobReconciler) {
	h.jobReconciler = reconciler
}

// ReconcileJobResponse reports whether a job was matched, skipped, or left
// untouched. It carries counts and a skip reason only, never any embedding
// data or the identity of a candidate that did not match.
type ReconcileJobResponse struct {
	Matched         bool   `json:"matched"`
	MappingsWritten int    `json:"mappings_written"`
	Skipped         bool   `json:"skipped"`
	SkipReason      string `json:"skip_reason,omitempty"`
}

// ReconcileJobSpeakers re-runs speaker identification for one completed job
// against whatever speaker profiles are enrolled right now.
// @Summary Reconcile one completed job against enrolled speaker profiles
// @Description Re-runs speaker identification for a single completed transcription job against the currently enrolled speaker profiles. Never blocks, fails, or re-queues the job: a job whose audio file no longer exists is reported as a skip, not an error.
// @Tags speakers
// @Produce json
// @Param job_id path string true "Transcription Job ID"
// @Success 200 {object} ReconcileJobResponse
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/speakers/reconcile/jobs/{job_id} [post]
func (h *Handler) ReconcileJobSpeakers(c *gin.Context) {
	jobID := c.Param("job_id")

	result, err := h.jobReconciler.ReconcileJob(c.Request.Context(), jobID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Transcription job not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reconcile job against enrolled speaker profiles"})
		return
	}

	c.JSON(http.StatusOK, ReconcileJobResponse{
		Matched:         result.Matched,
		MappingsWritten: result.MappingsWritten,
		Skipped:         result.Skipped,
		SkipReason:      result.SkipReason,
	})
}

// maxBatchReconcileLimit is the hard cap on how many jobs a single batch
// reconciliation call may consider. A request above the cap is rejected
// outright rather than silently clamped, so a caller always knows the size
// of the sweep it actually triggered.
const maxBatchReconcileLimit = 100

// defaultBatchReconcileLimit is used when a batch request omits limit.
const defaultBatchReconcileLimit = 20

// ReconcileBatchRequest is the optional request body for batch
// reconciliation. Limit defaults to defaultBatchReconcileLimit when omitted
// or zero.
type ReconcileBatchRequest struct {
	Limit int `json:"limit"`
}

// ReconcileBatchResponse reports the outcome of one bounded batch
// reconciliation run. It carries counts and job identifiers only, never any
// embedding data or the identity of a candidate that did not match.
type ReconcileBatchResponse struct {
	JobsConsidered      int      `json:"jobs_considered"`
	ProcessedJobIDs     []string `json:"processed_job_ids"`
	JobsMatched         int      `json:"jobs_matched"`
	SkippedMissingAudio []string `json:"skipped_missing_audio"`
	MappingsWritten     int      `json:"mappings_written"`
}

// ReconcileBatchSpeakers reconciles a bounded, oldest-first batch of
// completed, diarized jobs against whatever speaker profiles are enrolled
// right now, running one job at a time so the sweep never contends with live
// transcription for the shared embedding extractor.
// @Summary Reconcile a bounded batch of completed jobs against enrolled speaker profiles
// @Description Selects up to limit completed, diarized jobs, oldest first, and reconciles them one at a time against the currently enrolled speaker profiles. A job whose audio file no longer exists is skipped, not treated as an error, and the rest of the batch still runs. limit defaults to 20 and is capped at 100; a request above the cap is rejected before any processing starts.
// @Tags speakers
// @Accept json
// @Produce json
// @Param request body ReconcileBatchRequest false "Batch reconciliation options"
// @Success 200 {object} ReconcileBatchResponse
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/speakers/reconcile/batch [post]
func (h *Handler) ReconcileBatchSpeakers(c *gin.Context) {
	var request ReconcileBatchRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
			return
		}
	}

	limit := request.Limit
	if limit == 0 {
		limit = defaultBatchReconcileLimit
	}
	if limit > maxBatchReconcileLimit {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("limit must not exceed %d; it was rejected, not clamped", maxBatchReconcileLimit),
		})
		return
	}

	result, err := h.jobReconciler.ReconcileBatch(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reconcile batch against enrolled speaker profiles"})
		return
	}

	c.JSON(http.StatusOK, ReconcileBatchResponse{
		JobsConsidered:      result.JobsConsidered,
		ProcessedJobIDs:     result.ProcessedJobIDs,
		JobsMatched:         result.JobsMatched,
		SkippedMissingAudio: result.SkippedMissingAudio,
		MappingsWritten:     result.MappingsWritten,
	})
}
