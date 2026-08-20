package transcription

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"testing"

	"scriberr/internal/models"
	"scriberr/internal/transcription/interfaces"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const (
	identificationTestThreshold = 0.5
	identificationTestMargin    = 0.1
)

type stubSpeakerEmbeddingExtractor struct {
	resultsByLabel map[string]*interfaces.SpeakerEmbeddingResult
	err            error
	callsByLabel   map[string]int
}

func (s *stubSpeakerEmbeddingExtractor) ExtractSpeakerEmbedding(ctx context.Context, audioPath string, segments []interfaces.SpeakerSegment) (*interfaces.SpeakerEmbeddingResult, error) {
	if len(segments) == 0 {
		return nil, fmt.Errorf("extractor invoked with no segments")
	}
	label := segments[0].Speaker
	if s.callsByLabel == nil {
		s.callsByLabel = map[string]int{}
	}
	s.callsByLabel[label]++
	if s.err != nil {
		return nil, s.err
	}
	return s.resultsByLabel[label], nil
}

func (s *stubSpeakerEmbeddingExtractor) totalCalls() int {
	total := 0
	for _, count := range s.callsByLabel {
		total += count
	}
	return total
}

func encodeTestEmbedding(values []float32) []byte {
	encoded := make([]byte, 4*len(values))
	for i, value := range values {
		binary.LittleEndian.PutUint32(encoded[4*i:], math.Float32bits(value))
	}
	return encoded
}

func unitEmbeddingResult(x, y float64) *interfaces.SpeakerEmbeddingResult {
	length := math.Sqrt(x*x + y*y)
	return &interfaces.SpeakerEmbeddingResult{
		Dimensions:   2,
		Embedding:    encodeTestEmbedding([]float32{float32(x / length), float32(y / length)}),
		SecondsUsed:  20,
		SegmentsUsed: 2,
	}
}

func newIdentificationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&models.TranscriptionJob{},
		&models.MultiTrackFile{},
		&models.SpeakerMapping{},
		&models.SpeakerProfile{},
		&models.SpeakerProfileSample{},
	))
	return db
}

func seedIdentificationProfile(t *testing.T, db *gorm.DB, name string, sampleVectors ...[]float32) models.SpeakerProfile {
	t.Helper()
	profile := models.SpeakerProfile{Name: name}
	require.NoError(t, db.Create(&profile).Error)
	for _, vector := range sampleVectors {
		sample := models.SpeakerProfileSample{
			SpeakerProfileID:   profile.ID,
			Embedding:          encodeTestEmbedding(vector),
			Dimensions:         len(vector),
			Source:             "enrollment",
			SourceSpeakerLabel: "SPEAKER_00",
			SecondsUsed:        30,
		}
		require.NoError(t, db.Create(&sample).Error)
	}
	return profile
}

func seedIdentificationJob(t *testing.T, db *gorm.DB, executionPath string) *models.TranscriptionJob {
	t.Helper()
	job := models.TranscriptionJob{
		ID:            "identify-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-") + "-" + executionPath,
		AudioPath:     "/srv/audio/identify.wav",
		Diarization:   true,
		ExecutionPath: executionPath,
		Status:        models.StatusCompleted,
	}
	require.NoError(t, db.Create(&job).Error)
	return &job
}

func diarizedResult(labels ...string) *interfaces.TranscriptResult {
	result := &interfaces.TranscriptResult{}
	for i, label := range labels {
		speaker := label
		result.Segments = append(result.Segments,
			interfaces.TranscriptSegment{Start: float64(20 * i), End: float64(20*i + 9), Text: "first", Speaker: &speaker},
			interfaces.TranscriptSegment{Start: float64(20*i + 10), End: float64(20*i + 19), Text: "second", Speaker: &speaker},
		)
	}
	return result
}

func loadMappings(t *testing.T, db *gorm.DB, jobID string) []models.SpeakerMapping {
	t.Helper()
	var mappings []models.SpeakerMapping
	require.NoError(t, db.Where("transcription_job_id = ?", jobID).Order("original_speaker asc").Find(&mappings).Error)
	return mappings
}

func TestIdentifySpeakersConfidentMatchWritesAutoMapping(t *testing.T) {
	db := newIdentificationTestDB(t)
	profile := seedIdentificationProfile(t, db, "Ada", []float32{1, 0})
	job := seedIdentificationJob(t, db, "local")
	extractor := &stubSpeakerEmbeddingExtractor{resultsByLabel: map[string]*interfaces.SpeakerEmbeddingResult{
		"SPEAKER_00": unitEmbeddingResult(0.9848, 0.1736),
	}}

	identifier := NewSpeakerIdentifier(db, extractor, identificationTestThreshold, identificationTestMargin)
	_ = identifier.IdentifySpeakers(context.Background(), job, diarizedResult("SPEAKER_00"))

	mappings := loadMappings(t, db, job.ID)
	require.Len(t, mappings, 1)
	require.Equal(t, "SPEAKER_00", mappings[0].OriginalSpeaker)
	require.Equal(t, "Ada", mappings[0].CustomName)
	require.Equal(t, "auto", mappings[0].Source)
	require.NotNil(t, mappings[0].SpeakerProfileID)
	require.Equal(t, profile.ID, *mappings[0].SpeakerProfileID)
	require.NotNil(t, mappings[0].Confidence)
	require.InDelta(t, 0.9848, *mappings[0].Confidence, 0.01)
}

func TestIdentifySpeakersBelowThresholdWritesNothing(t *testing.T) {
	db := newIdentificationTestDB(t)
	seedIdentificationProfile(t, db, "Ada", []float32{1, 0})
	job := seedIdentificationJob(t, db, "local")
	extractor := &stubSpeakerEmbeddingExtractor{resultsByLabel: map[string]*interfaces.SpeakerEmbeddingResult{
		"SPEAKER_00": unitEmbeddingResult(0.3, 0.954),
	}}

	identifier := NewSpeakerIdentifier(db, extractor, identificationTestThreshold, identificationTestMargin)
	_ = identifier.IdentifySpeakers(context.Background(), job, diarizedResult("SPEAKER_00"))

	require.Empty(t, loadMappings(t, db, job.ID))
}

func TestIdentifySpeakersAmbiguousMarginWritesNothing(t *testing.T) {
	db := newIdentificationTestDB(t)
	seedIdentificationProfile(t, db, "Ada", []float32{1, 0})
	seedIdentificationProfile(t, db, "Keno", []float32{0, 1})
	job := seedIdentificationJob(t, db, "local")
	extractor := &stubSpeakerEmbeddingExtractor{resultsByLabel: map[string]*interfaces.SpeakerEmbeddingResult{
		"SPEAKER_00": unitEmbeddingResult(0.7071, 0.7071),
	}}

	identifier := NewSpeakerIdentifier(db, extractor, identificationTestThreshold, identificationTestMargin)
	_ = identifier.IdentifySpeakers(context.Background(), job, diarizedResult("SPEAKER_00"))

	require.Empty(t, loadMappings(t, db, job.ID))
}

func TestIdentifySpeakersNeverOverwritesExistingMapping(t *testing.T) {
	db := newIdentificationTestDB(t)
	seedIdentificationProfile(t, db, "Ada", []float32{1, 0})
	job := seedIdentificationJob(t, db, "local")
	existing := models.SpeakerMapping{
		TranscriptionJobID: job.ID,
		OriginalSpeaker:    "SPEAKER_00",
		CustomName:         "Grace",
		Source:             "manual",
	}
	require.NoError(t, db.Create(&existing).Error)
	extractor := &stubSpeakerEmbeddingExtractor{resultsByLabel: map[string]*interfaces.SpeakerEmbeddingResult{
		"SPEAKER_00": unitEmbeddingResult(0.9848, 0.1736),
	}}

	identifier := NewSpeakerIdentifier(db, extractor, identificationTestThreshold, identificationTestMargin)
	_ = identifier.IdentifySpeakers(context.Background(), job, diarizedResult("SPEAKER_00"))

	mappings := loadMappings(t, db, job.ID)
	require.Len(t, mappings, 1)
	require.Equal(t, "Grace", mappings[0].CustomName)
	require.Equal(t, "manual", mappings[0].Source)
	require.Nil(t, mappings[0].SpeakerProfileID)
}

func TestIdentifySpeakersExtractorErrorLeavesJobIntact(t *testing.T) {
	db := newIdentificationTestDB(t)
	seedIdentificationProfile(t, db, "Ada", []float32{1, 0})
	job := seedIdentificationJob(t, db, "local")
	extractor := &stubSpeakerEmbeddingExtractor{err: fmt.Errorf("extractor exploded")}

	identifier := NewSpeakerIdentifier(db, extractor, identificationTestThreshold, identificationTestMargin)
	_ = identifier.IdentifySpeakers(context.Background(), job, diarizedResult("SPEAKER_00"))

	require.Empty(t, loadMappings(t, db, job.ID))
	var persisted models.TranscriptionJob
	require.NoError(t, db.First(&persisted, "id = ?", job.ID).Error)
	require.Equal(t, models.StatusCompleted, persisted.Status)
}

func TestIdentifySpeakersNoEnrolledSamplesSkipsExtraction(t *testing.T) {
	db := newIdentificationTestDB(t)
	seedIdentificationProfile(t, db, "Empty Profile")
	job := seedIdentificationJob(t, db, "local")
	extractor := &stubSpeakerEmbeddingExtractor{}

	identifier := NewSpeakerIdentifier(db, extractor, identificationTestThreshold, identificationTestMargin)
	_ = identifier.IdentifySpeakers(context.Background(), job, diarizedResult("SPEAKER_00", "SPEAKER_01"))

	require.Zero(t, extractor.totalCalls(), "extractor must never run when no profile has a sample")
	require.Empty(t, loadMappings(t, db, job.ID))
}

func TestIdentifySpeakersExecutionPathEquivalence(t *testing.T) {
	executionPaths := []string{"remote", "local", "local-fallback"}
	type observedMapping struct {
		OriginalSpeaker string
		CustomName      string
		Source          string
	}
	resultsByPath := map[string][]observedMapping{}
	for _, executionPath := range executionPaths {
		t.Run(executionPath, func(t *testing.T) {
			db := newIdentificationTestDB(t)
			seedIdentificationProfile(t, db, "Ada", []float32{1, 0})
			job := seedIdentificationJob(t, db, executionPath)
			extractor := &stubSpeakerEmbeddingExtractor{resultsByLabel: map[string]*interfaces.SpeakerEmbeddingResult{
				"SPEAKER_00": unitEmbeddingResult(0.9848, 0.1736),
				"SPEAKER_01": unitEmbeddingResult(0.2, 0.9798),
			}}
			identifier := NewSpeakerIdentifier(db, extractor, identificationTestThreshold, identificationTestMargin)
			_ = identifier.IdentifySpeakers(context.Background(), job, diarizedResult("SPEAKER_00", "SPEAKER_01"))
			for _, mapping := range loadMappings(t, db, job.ID) {
				resultsByPath[executionPath] = append(resultsByPath[executionPath], observedMapping{
					OriginalSpeaker: mapping.OriginalSpeaker,
					CustomName:      mapping.CustomName,
					Source:          mapping.Source,
				})
			}
		})
	}
	require.Equal(t, resultsByPath["local"], resultsByPath["remote"])
	require.Equal(t, resultsByPath["local"], resultsByPath["local-fallback"])
	require.Len(t, resultsByPath["local"], 1)
}

func TestIdentifySpeakersOneProfileClaimsEveryClearingLabel(t *testing.T) {
	db := newIdentificationTestDB(t)
	profile := seedIdentificationProfile(t, db, "Ada", []float32{1, 0})
	job := seedIdentificationJob(t, db, "local")
	extractor := &stubSpeakerEmbeddingExtractor{resultsByLabel: map[string]*interfaces.SpeakerEmbeddingResult{
		"SPEAKER_00": unitEmbeddingResult(0.9848, 0.1736),
		"SPEAKER_03": unitEmbeddingResult(0.9397, 0.342),
	}}

	identifier := NewSpeakerIdentifier(db, extractor, identificationTestThreshold, identificationTestMargin)
	_ = identifier.IdentifySpeakers(context.Background(), job, diarizedResult("SPEAKER_00", "SPEAKER_03"))

	mappings := loadMappings(t, db, job.ID)
	require.Len(t, mappings, 2, "an over-split speaker keeps every label that clears threshold and margin")
	for _, mapping := range mappings {
		require.Equal(t, "auto", mapping.Source)
		require.Equal(t, "Ada", mapping.CustomName)
		require.NotNil(t, mapping.SpeakerProfileID)
		require.Equal(t, profile.ID, *mapping.SpeakerProfileID)
	}
}
