package api

import (
	"errors"
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
