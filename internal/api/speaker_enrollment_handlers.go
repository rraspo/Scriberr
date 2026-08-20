package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"scriberr/internal/models"
	"scriberr/internal/transcription/interfaces"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// SpeakerSegment, SpeakerEmbeddingResult and SpeakerEmbeddingExtractor are
// aliased from the transcription/interfaces package rather than defined
// here so that adapters implementing SpeakerEmbeddingExtractor do not need
// to import this package, which would create an import cycle (api already
// imports transcription, which adapters' tests import adapters from).
type SpeakerSegment = interfaces.SpeakerSegment
type SpeakerEmbeddingResult = interfaces.SpeakerEmbeddingResult
type SpeakerEmbeddingExtractor = interfaces.SpeakerEmbeddingExtractor

// SetSpeakerEmbeddingExtractor wires the speaker embedding extractor into
// the handler. It is a separate setter rather than a NewHandler parameter,
// following the same pattern as SetSpeakerProfileRepo, so that NewHandler's
// existing positional call sites do not need to change.
func (h *Handler) SetSpeakerEmbeddingExtractor(extractor SpeakerEmbeddingExtractor) {
	h.speakerEmbeddingExtractor = extractor
}

// EnrollSpeakerProfileSampleRequest identifies the finished job and the
// diarized speaker label within it to enroll as a new sample.
type EnrollSpeakerProfileSampleRequest struct {
	JobID        string `json:"job_id" binding:"required"`
	SpeakerLabel string `json:"speaker_label" binding:"required"`
}

// transcriptSegmentJSON mirrors one entry of a job's stored transcript JSON.
type transcriptSegmentJSON struct {
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Text    string  `json:"text"`
	Speaker string  `json:"speaker"`
}

// jobTranscriptJSON mirrors the shape stored on TranscriptionJob.Transcript.
type jobTranscriptJSON struct {
	Segments []transcriptSegmentJSON `json:"segments"`
}

// EnrollSpeakerProfileSample enrolls a new voice sample against a speaker
// profile by isolating one diarized speaker's segments from a finished
// job's transcript and running the embedding extractor over them.
// @Summary Enroll a speaker profile sample from a job
// @Description Loads a finished job's diarized transcript, isolates the requested speaker label's segments, and enrolls a new voice sample against the profile from that speaker's audio
// @Tags speakers
// @Accept json
// @Produce json
// @Param id path string true "Speaker Profile ID"
// @Param request body EnrollSpeakerProfileSampleRequest true "Job and speaker label to enroll"
// @Success 201 {object} SpeakerProfileSampleResponse
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 422 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/speakers/profiles/{id}/samples [post]
func (h *Handler) EnrollSpeakerProfileSample(c *gin.Context) {
	profileID, ok := parseSpeakerProfilePathID(c, "id")
	if !ok {
		return
	}

	var req EnrollSpeakerProfileSampleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	profile, err := h.speakerProfileRepo.FindByID(c.Request.Context(), profileID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Speaker profile not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get speaker profile"})
		return
	}

	job, err := h.jobRepo.FindByID(c.Request.Context(), req.JobID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Transcription job not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get transcription job"})
		return
	}

	var transcript jobTranscriptJSON
	if job.Transcript != nil {
		if err := json.Unmarshal([]byte(*job.Transcript), &transcript); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse job transcript"})
			return
		}
	}

	var segments []SpeakerSegment
	for _, segment := range transcript.Segments {
		if segment.Speaker != req.SpeakerLabel {
			continue
		}
		segments = append(segments, SpeakerSegment{Speaker: segment.Speaker, Start: segment.Start, End: segment.End})
	}
	if len(segments) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Speaker label %q was not found in this job's transcript", req.SpeakerLabel)})
		return
	}

	result, err := h.speakerEmbeddingExtractor.ExtractSpeakerEmbedding(c.Request.Context(), job.AudioPath, segments)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to extract speaker embedding: " + err.Error()})
		return
	}
	if result.Embedding == nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "Not enough audio for this speaker to enroll a voice sample"})
		return
	}

	sample := models.SpeakerProfileSample{
		SpeakerProfileID:   profile.ID,
		Embedding:          result.Embedding,
		Dimensions:         result.Dimensions,
		Source:             "enrollment",
		SourceJobID:        job.ID,
		SourceSpeakerLabel: req.SpeakerLabel,
		SecondsUsed:        result.SecondsUsed,
	}
	if err := h.speakerProfileRepo.CreateSample(c.Request.Context(), &sample); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save speaker profile sample"})
		return
	}

	c.JSON(http.StatusCreated, speakerProfileSampleToResponse(sample))
}
