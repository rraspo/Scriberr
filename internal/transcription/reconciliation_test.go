package transcription

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scriberr/internal/models"
	"scriberr/internal/transcription/interfaces"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func writeReconciliationAudioFile(t *testing.T) string {
	t.Helper()
	audioPath := filepath.Join(t.TempDir(), "reconcile.wav")
	require.NoError(t, os.WriteFile(audioPath, []byte("not-a-real-wav"), 0o644))
	return audioPath
}

func seedReconciliationJob(t *testing.T, db *gorm.DB, audioPath string, transcript *interfaces.TranscriptResult) *models.TranscriptionJob {
	t.Helper()
	transcriptJSON, err := json.Marshal(transcript)
	require.NoError(t, err)
	transcriptText := string(transcriptJSON)
	job := models.TranscriptionJob{
		ID:          "reconcile-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-"),
		AudioPath:   audioPath,
		Diarization: true,
		Status:      models.StatusCompleted,
		Transcript:  &transcriptText,
	}
	require.NoError(t, db.Create(&job).Error)
	return &job
}

func TestReconcileJobWritesAutoMappingForCompletedJob(t *testing.T) {
	db := newIdentificationTestDB(t)
	profile := seedIdentificationProfile(t, db, "Ada", []float32{1, 0})
	job := seedReconciliationJob(t, db, writeReconciliationAudioFile(t), diarizedResult("SPEAKER_00"))
	extractor := &stubSpeakerEmbeddingExtractor{resultsByLabel: map[string]*interfaces.SpeakerEmbeddingResult{
		"SPEAKER_00": unitEmbeddingResult(0.9848, 0.1736),
	}}
	identifier := NewSpeakerIdentifier(db, extractor, identificationTestThreshold, identificationTestMargin)

	reconciler := NewJobReconciler(db, identifier)
	result, err := reconciler.ReconcileJob(context.Background(), job.ID)

	require.NoError(t, err)
	require.False(t, result.Skipped)
	require.True(t, result.Matched)
	require.Equal(t, 1, result.MappingsWritten)
	mappings := loadMappings(t, db, job.ID)
	require.Len(t, mappings, 1)
	require.Equal(t, "SPEAKER_00", mappings[0].OriginalSpeaker)
	require.Equal(t, "Ada", mappings[0].CustomName)
	require.Equal(t, "auto", mappings[0].Source)
	require.NotNil(t, mappings[0].SpeakerProfileID)
	require.Equal(t, profile.ID, *mappings[0].SpeakerProfileID)
}

func TestReconcileJobSecondRunWritesNoAdditionalRows(t *testing.T) {
	db := newIdentificationTestDB(t)
	seedIdentificationProfile(t, db, "Ada", []float32{1, 0})
	job := seedReconciliationJob(t, db, writeReconciliationAudioFile(t), diarizedResult("SPEAKER_00"))
	extractor := &stubSpeakerEmbeddingExtractor{resultsByLabel: map[string]*interfaces.SpeakerEmbeddingResult{
		"SPEAKER_00": unitEmbeddingResult(0.9848, 0.1736),
	}}
	identifier := NewSpeakerIdentifier(db, extractor, identificationTestThreshold, identificationTestMargin)
	reconciler := NewJobReconciler(db, identifier)

	first, err := reconciler.ReconcileJob(context.Background(), job.ID)
	require.NoError(t, err)
	require.Equal(t, 1, first.MappingsWritten)

	second, err := reconciler.ReconcileJob(context.Background(), job.ID)
	require.NoError(t, err)
	require.Equal(t, 0, second.MappingsWritten)
	require.Len(t, loadMappings(t, db, job.ID), 1)
}

func TestReconcileJobMissingAudioIsSkippedNotError(t *testing.T) {
	db := newIdentificationTestDB(t)
	seedIdentificationProfile(t, db, "Ada", []float32{1, 0})
	missingPath := filepath.Join(t.TempDir(), "gone.wav")
	job := seedReconciliationJob(t, db, missingPath, diarizedResult("SPEAKER_00"))
	extractor := &stubSpeakerEmbeddingExtractor{resultsByLabel: map[string]*interfaces.SpeakerEmbeddingResult{
		"SPEAKER_00": unitEmbeddingResult(0.9848, 0.1736),
	}}
	identifier := NewSpeakerIdentifier(db, extractor, identificationTestThreshold, identificationTestMargin)

	reconciler := NewJobReconciler(db, identifier)
	result, err := reconciler.ReconcileJob(context.Background(), job.ID)

	require.NoError(t, err, "missing audio is a reported skip, never an error")
	require.True(t, result.Skipped)
	require.NotEmpty(t, result.SkipReason)
	require.Zero(t, extractor.totalCalls(), "extractor must not run when the audio file is gone")
	require.Empty(t, loadMappings(t, db, job.ID))
}

func TestReconcileJobNeverOverwritesManualMapping(t *testing.T) {
	db := newIdentificationTestDB(t)
	seedIdentificationProfile(t, db, "Ada", []float32{1, 0})
	job := seedReconciliationJob(t, db, writeReconciliationAudioFile(t), diarizedResult("SPEAKER_00"))
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

	reconciler := NewJobReconciler(db, identifier)
	_, err := reconciler.ReconcileJob(context.Background(), job.ID)

	require.NoError(t, err)
	mappings := loadMappings(t, db, job.ID)
	require.Len(t, mappings, 1)
	require.Equal(t, "Grace", mappings[0].CustomName)
	require.Equal(t, "manual", mappings[0].Source)
	require.Nil(t, mappings[0].SpeakerProfileID)
}
