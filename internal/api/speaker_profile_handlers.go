package api

import (
	"net/http"
	"strconv"
	"time"

	"scriberr/internal/models"
	"scriberr/internal/repository"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// SetSpeakerProfileRepo wires the speaker profile repository into the
// handler. It is a separate setter rather than a NewHandler parameter so
// that NewHandler's existing positional call sites do not need to change.
func (h *Handler) SetSpeakerProfileRepo(repo repository.SpeakerProfileRepository) {
	h.speakerProfileRepo = repo
}

// CreateSpeakerProfileRequest represents a speaker profile creation request
type CreateSpeakerProfileRequest struct {
	Name  string `json:"name" binding:"required"`
	Notes string `json:"notes"`
}

// UpdateSpeakerProfileRequest represents a speaker profile update request.
// Both fields are optional so a caller can rename, update notes, or both.
type UpdateSpeakerProfileRequest struct {
	Name  *string `json:"name,omitempty"`
	Notes *string `json:"notes,omitempty"`
}

// SpeakerProfileResponse is the API representation of a speaker profile.
// It never carries any sample data, so an embedding can never reach a
// response through this type.
type SpeakerProfileResponse struct {
	ID          uint      `json:"id"`
	Name        string    `json:"name"`
	Notes       string    `json:"notes"`
	SampleCount int64     `json:"sample_count"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// SpeakerProfileSampleResponse is the API representation of a speaker
// profile sample. It deliberately excludes the embedding and its dimensions.
type SpeakerProfileSampleResponse struct {
	ID                 uint      `json:"id"`
	Source             string    `json:"source"`
	SourceSpeakerLabel string    `json:"source_speaker_label"`
	SecondsUsed        float64   `json:"seconds_used"`
	CreatedAt          time.Time `json:"created_at"`
}

func speakerProfileToResponse(profile models.SpeakerProfile, sampleCount int64) SpeakerProfileResponse {
	return SpeakerProfileResponse{
		ID:          profile.ID,
		Name:        profile.Name,
		Notes:       profile.Notes,
		SampleCount: sampleCount,
		UpdatedAt:   profile.UpdatedAt,
	}
}

func parseSpeakerProfilePathID(c *gin.Context, param string) (uint, bool) {
	raw := c.Param(param)
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid speaker profile identifier"})
		return 0, false
	}
	return uint(value), true
}

// CreateSpeakerProfile creates a new speaker profile
// @Summary Create a speaker profile
// @Description Creates a new named speaker profile that samples can later be enrolled against
// @Tags speakers
// @Accept json
// @Produce json
// @Param request body CreateSpeakerProfileRequest true "Speaker profile to create"
// @Success 201 {object} SpeakerProfileResponse
// @Failure 400 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/speakers/profiles [post]
func (h *Handler) CreateSpeakerProfile(c *gin.Context) {
	var req CreateSpeakerProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	if _, err := h.speakerProfileRepo.FindByName(c.Request.Context(), req.Name); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "A speaker profile with this name already exists"})
		return
	} else if err != gorm.ErrRecordNotFound {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check existing speaker profiles"})
		return
	}

	profile := models.SpeakerProfile{Name: req.Name, Notes: req.Notes}
	if err := h.speakerProfileRepo.Create(c.Request.Context(), &profile); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create speaker profile"})
		return
	}

	c.JSON(http.StatusCreated, speakerProfileToResponse(profile, 0))
}

// ListSpeakerProfiles lists all speaker profiles
// @Summary List speaker profiles
// @Description Lists every speaker profile along with how many samples are enrolled against it
// @Tags speakers
// @Produce json
// @Success 200 {array} SpeakerProfileResponse
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/speakers/profiles [get]
func (h *Handler) ListSpeakerProfiles(c *gin.Context) {
	profiles, err := h.speakerProfileRepo.ListWithSampleCounts(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list speaker profiles"})
		return
	}

	response := make([]SpeakerProfileResponse, len(profiles))
	for i, profile := range profiles {
		response[i] = speakerProfileToResponse(profile.SpeakerProfile, profile.SampleCount)
	}

	c.JSON(http.StatusOK, response)
}

// UpdateSpeakerProfile renames and/or updates the notes of a speaker profile
// @Summary Update a speaker profile
// @Description Renames a speaker profile and/or updates its notes
// @Tags speakers
// @Accept json
// @Produce json
// @Param id path string true "Speaker Profile ID"
// @Param request body UpdateSpeakerProfileRequest true "Fields to update"
// @Success 200 {object} SpeakerProfileResponse
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/speakers/profiles/{id} [patch]
func (h *Handler) UpdateSpeakerProfile(c *gin.Context) {
	id, ok := parseSpeakerProfilePathID(c, "id")
	if !ok {
		return
	}

	var req UpdateSpeakerProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	profile, err := h.speakerProfileRepo.FindByID(c.Request.Context(), id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Speaker profile not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get speaker profile"})
		return
	}

	if req.Name != nil && *req.Name != profile.Name {
		existing, err := h.speakerProfileRepo.FindByName(c.Request.Context(), *req.Name)
		if err == nil && existing.ID != profile.ID {
			c.JSON(http.StatusConflict, gin.H{"error": "A speaker profile with this name already exists"})
			return
		} else if err != nil && err != gorm.ErrRecordNotFound {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check existing speaker profiles"})
			return
		}
		profile.Name = *req.Name
	}
	if req.Notes != nil {
		profile.Notes = *req.Notes
	}

	if err := h.speakerProfileRepo.Update(c.Request.Context(), profile); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update speaker profile"})
		return
	}

	sampleCount, err := h.speakerProfileRepo.CountSamples(c.Request.Context(), profile.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to count speaker profile samples"})
		return
	}

	c.JSON(http.StatusOK, speakerProfileToResponse(*profile, sampleCount))
}

// DeleteSpeakerProfile deletes a speaker profile and all its samples
// @Summary Delete a speaker profile
// @Description Deletes a speaker profile along with every sample enrolled against it
// @Tags speakers
// @Produce json
// @Param id path string true "Speaker Profile ID"
// @Success 200 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/speakers/profiles/{id} [delete]
func (h *Handler) DeleteSpeakerProfile(c *gin.Context) {
	id, ok := parseSpeakerProfilePathID(c, "id")
	if !ok {
		return
	}

	if _, err := h.speakerProfileRepo.FindByID(c.Request.Context(), id); err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Speaker profile not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get speaker profile"})
		return
	}

	if err := h.speakerProfileRepo.DeleteWithSamples(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete speaker profile"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Speaker profile deleted successfully"})
}

// ListSpeakerProfileSamples lists the samples enrolled against a speaker profile
// @Summary List speaker profile samples
// @Description Lists the samples enrolled against a speaker profile, never including embedding data
// @Tags speakers
// @Produce json
// @Param id path string true "Speaker Profile ID"
// @Success 200 {array} SpeakerProfileSampleResponse
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/speakers/profiles/{id}/samples [get]
func (h *Handler) ListSpeakerProfileSamples(c *gin.Context) {
	id, ok := parseSpeakerProfilePathID(c, "id")
	if !ok {
		return
	}

	if _, err := h.speakerProfileRepo.FindByID(c.Request.Context(), id); err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Speaker profile not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get speaker profile"})
		return
	}

	samples, err := h.speakerProfileRepo.ListSamples(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list speaker profile samples"})
		return
	}

	response := make([]SpeakerProfileSampleResponse, len(samples))
	for i, sample := range samples {
		response[i] = SpeakerProfileSampleResponse{
			ID:                 sample.ID,
			Source:             sample.Source,
			SourceSpeakerLabel: sample.SourceSpeakerLabel,
			SecondsUsed:        sample.SecondsUsed,
			CreatedAt:          sample.CreatedAt,
		}
	}

	c.JSON(http.StatusOK, response)
}

// DeleteSpeakerProfileSample deletes a single sample from a speaker profile
// @Summary Delete a speaker profile sample
// @Description Deletes a single sample from a speaker profile, leaving the profile and its other samples intact
// @Tags speakers
// @Produce json
// @Param id path string true "Speaker Profile ID"
// @Param sample_id path string true "Speaker Profile Sample ID"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/speakers/profiles/{id}/samples/{sample_id} [delete]
func (h *Handler) DeleteSpeakerProfileSample(c *gin.Context) {
	profileID, ok := parseSpeakerProfilePathID(c, "id")
	if !ok {
		return
	}
	sampleID, ok := parseSpeakerProfilePathID(c, "sample_id")
	if !ok {
		return
	}

	if err := h.speakerProfileRepo.DeleteSample(c.Request.Context(), profileID, sampleID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete speaker profile sample"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Speaker profile sample deleted successfully"})
}
