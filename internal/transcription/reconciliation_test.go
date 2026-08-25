package transcription

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

// Batch reconciliation contract: ReconcileBatch(ctx, limit) walks up to limit
// completed, diarized jobs oldest-first, calling ReconcileJob one at a time.
// BatchReconcileResult exposes at least JobsConsidered (int), ProcessedJobIDs
// ([]string, selection order), JobsMatched (int), SkippedMissingAudio
// ([]string of job ids), MappingsWritten (int).

type countingSpeakerEmbeddingExtractor struct {
	inner           stubSpeakerEmbeddingExtractor
	delay           time.Duration
	mutex           sync.Mutex
	inFlight        int
	peakConcurrency int
	totalCallCount  int
}

func (extractor *countingSpeakerEmbeddingExtractor) ExtractSpeakerEmbedding(ctx context.Context, audioPath string, segments []interfaces.SpeakerSegment) (*interfaces.SpeakerEmbeddingResult, error) {
	extractor.mutex.Lock()
	extractor.inFlight++
	extractor.totalCallCount++
	if extractor.inFlight > extractor.peakConcurrency {
		extractor.peakConcurrency = extractor.inFlight
	}
	extractor.mutex.Unlock()

	if extractor.delay > 0 {
		time.Sleep(extractor.delay)
	}
	result, err := extractor.inner.ExtractSpeakerEmbedding(ctx, audioPath, segments)

	extractor.mutex.Lock()
	extractor.inFlight--
	extractor.mutex.Unlock()
	return result, err
}

func seedBatchJob(t *testing.T, db *gorm.DB, index int, audioPath string, transcript *interfaces.TranscriptResult, diarized bool) *models.TranscriptionJob {
	t.Helper()
	transcriptJSON, err := json.Marshal(transcript)
	require.NoError(t, err)
	transcriptText := string(transcriptJSON)
	job := models.TranscriptionJob{
		ID:          fmt.Sprintf("batch-%s-%02d", strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-"), index),
		AudioPath:   audioPath,
		Diarization: diarized,
		Status:      models.StatusCompleted,
		Transcript:  &transcriptText,
		CreatedAt:   time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC).Add(time.Duration(index) * time.Minute),
	}
	require.NoError(t, db.Create(&job).Error)
	return &job
}

func matchingBatchExtractor() *countingSpeakerEmbeddingExtractor {
	return &countingSpeakerEmbeddingExtractor{
		inner: stubSpeakerEmbeddingExtractor{resultsByLabel: map[string]*interfaces.SpeakerEmbeddingResult{
			"SPEAKER_00": unitEmbeddingResult(0.9848, 0.1736),
		}},
	}
}

func TestReconcileBatchHonorsLimitOldestFirstAndReportsWhich(t *testing.T) {
	db := newIdentificationTestDB(t)
	seedIdentificationProfile(t, db, "Ada", []float32{1, 0})
	var jobIDs []string
	for index := 0; index < 5; index++ {
		job := seedBatchJob(t, db, index, writeReconciliationAudioFile(t), diarizedResult("SPEAKER_00"), true)
		jobIDs = append(jobIDs, job.ID)
	}
	extractor := matchingBatchExtractor()
	identifier := NewSpeakerIdentifier(db, extractor, identificationTestThreshold, identificationTestMargin)

	reconciler := NewJobReconciler(db, identifier)
	result, err := reconciler.ReconcileBatch(context.Background(), 2)

	require.NoError(t, err)
	require.Equal(t, 2, result.JobsConsidered)
	require.Equal(t, []string{jobIDs[0], jobIDs[1]}, result.ProcessedJobIDs, "the two oldest eligible jobs, in order")
	require.Equal(t, 2, result.JobsMatched)
	require.Equal(t, 2, result.MappingsWritten)
	require.Empty(t, result.SkippedMissingAudio)
	require.Empty(t, loadMappings(t, db, jobIDs[2]), "jobs beyond the limit are untouched")
}

func TestReconcileBatchSkipsMissingAudioWithoutAbortingTheRest(t *testing.T) {
	db := newIdentificationTestDB(t)
	seedIdentificationProfile(t, db, "Ada", []float32{1, 0})
	first := seedBatchJob(t, db, 0, writeReconciliationAudioFile(t), diarizedResult("SPEAKER_00"), true)
	gone := seedBatchJob(t, db, 1, filepath.Join(t.TempDir(), "gone.wav"), diarizedResult("SPEAKER_00"), true)
	last := seedBatchJob(t, db, 2, writeReconciliationAudioFile(t), diarizedResult("SPEAKER_00"), true)
	extractor := matchingBatchExtractor()
	identifier := NewSpeakerIdentifier(db, extractor, identificationTestThreshold, identificationTestMargin)

	reconciler := NewJobReconciler(db, identifier)
	result, err := reconciler.ReconcileBatch(context.Background(), 10)

	require.NoError(t, err, "a missing-audio job must not abort the batch")
	require.Equal(t, []string{gone.ID}, result.SkippedMissingAudio)
	require.Equal(t, 2, result.JobsMatched)
	require.Len(t, loadMappings(t, db, first.ID), 1)
	require.Len(t, loadMappings(t, db, last.ID), 1, "jobs after the skipped one still run")
	require.Empty(t, loadMappings(t, db, gone.ID))
}

func TestReconcileBatchRunsStrictlySequentially(t *testing.T) {
	db := newIdentificationTestDB(t)
	seedIdentificationProfile(t, db, "Ada", []float32{1, 0})
	for index := 0; index < 3; index++ {
		seedBatchJob(t, db, index, writeReconciliationAudioFile(t), diarizedResult("SPEAKER_00"), true)
	}
	extractor := matchingBatchExtractor()
	extractor.delay = 30 * time.Millisecond
	identifier := NewSpeakerIdentifier(db, extractor, identificationTestThreshold, identificationTestMargin)

	reconciler := NewJobReconciler(db, identifier)
	_, err := reconciler.ReconcileBatch(context.Background(), 10)

	require.NoError(t, err)
	require.Equal(t, 3, extractor.totalCallCount)
	require.Equal(t, 1, extractor.peakConcurrency, "extraction must never run concurrently within a batch")
}

func TestReconcileBatchExcludesNonDiarizedJobs(t *testing.T) {
	db := newIdentificationTestDB(t)
	seedIdentificationProfile(t, db, "Ada", []float32{1, 0})
	plain := seedBatchJob(t, db, 0, writeReconciliationAudioFile(t), diarizedResult(), false)
	eligible := seedBatchJob(t, db, 1, writeReconciliationAudioFile(t), diarizedResult("SPEAKER_00"), true)
	extractor := matchingBatchExtractor()
	identifier := NewSpeakerIdentifier(db, extractor, identificationTestThreshold, identificationTestMargin)

	reconciler := NewJobReconciler(db, identifier)
	result, err := reconciler.ReconcileBatch(context.Background(), 10)

	require.NoError(t, err)
	require.Equal(t, []string{eligible.ID}, result.ProcessedJobIDs, "non-diarized jobs are not candidates")
	require.Equal(t, 1, extractor.totalCallCount, "the extractor never runs for a non-diarized job")
	require.Empty(t, loadMappings(t, db, plain.ID))
}
