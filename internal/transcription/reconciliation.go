package transcription

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"gorm.io/gorm"

	"scriberr/internal/models"
	"scriberr/internal/transcription/interfaces"
	"scriberr/pkg/logger"
)

// ReconcileResult reports the outcome of one on-demand reconciliation call.
// It carries counts and a skip reason only, never embedding data or the
// identity of any candidate that did not match.
type ReconcileResult struct {
	Matched         bool
	MappingsWritten int
	Skipped         bool
	SkipReason      string
}

// JobReconciler re-runs speaker identification for one already-completed
// job, on demand, against whatever speaker profiles are enrolled at call
// time. It is a separate trigger over SpeakerIdentifier.IdentifySpeakers,
// not a second matching implementation: this file reconstructs the input
// IdentifySpeakers expects and never touches the matching logic itself.
type JobReconciler struct {
	db         *gorm.DB
	identifier *SpeakerIdentifier
}

// NewJobReconciler creates a job reconciler backed by db and identifier.
func NewJobReconciler(db *gorm.DB, identifier *SpeakerIdentifier) *JobReconciler {
	return &JobReconciler{db: db, identifier: identifier}
}

// ReconcileJob loads jobID, rebuilds the interfaces.TranscriptResult its
// persisted transcript JSON represents, confirms the job's audio is still on
// disk, then calls IdentifySpeakers unchanged. A job whose audio has since
// been removed is reported as a skip rather than an error or a failed job,
// since reconciliation must never block or re-queue the job it runs against.
func (reconciler *JobReconciler) ReconcileJob(ctx context.Context, jobID string) (ReconcileResult, error) {
	var job models.TranscriptionJob
	if err := reconciler.db.WithContext(ctx).First(&job, "id = ?", jobID).Error; err != nil {
		return ReconcileResult{}, fmt.Errorf("failed to load transcription job %q: %w", jobID, err)
	}

	var transcript interfaces.TranscriptResult
	if job.Transcript != nil && *job.Transcript != "" {
		if err := json.Unmarshal([]byte(*job.Transcript), &transcript); err != nil {
			return ReconcileResult{}, fmt.Errorf("failed to parse persisted transcript for job %q: %w", jobID, err)
		}
	}

	if _, err := os.Stat(job.AudioPath); err != nil {
		result := ReconcileResult{Skipped: true, SkipReason: "audio file is no longer available on disk"}
		logger.Info("Speaker reconciliation skipped", "job_id", jobID, "skip_reason", result.SkipReason)
		return result, nil
	}

	mappingsBefore, err := reconciler.countMappings(ctx, jobID)
	if err != nil {
		return ReconcileResult{}, err
	}

	if err := reconciler.identifier.IdentifySpeakers(ctx, &job, &transcript); err != nil {
		return ReconcileResult{}, fmt.Errorf("speaker reconciliation failed for job %q: %w", jobID, err)
	}

	mappingsAfter, err := reconciler.countMappings(ctx, jobID)
	if err != nil {
		return ReconcileResult{}, err
	}

	result := ReconcileResult{
		Matched:         mappingsAfter > mappingsBefore,
		MappingsWritten: mappingsAfter - mappingsBefore,
	}
	logger.Info("Speaker reconciliation completed",
		"job_id", jobID,
		"matched", result.Matched,
		"mappings_written", result.MappingsWritten,
	)
	return result, nil
}

// countMappings counts a job's speaker mappings regardless of source. It is
// how ReconcileJob measures what IdentifySpeakers wrote, since that call
// only ever adds rows and never reports a count of its own.
func (reconciler *JobReconciler) countMappings(ctx context.Context, jobID string) (int, error) {
	var count int64
	if err := reconciler.db.WithContext(ctx).
		Model(&models.SpeakerMapping{}).
		Where("transcription_job_id = ?", jobID).
		Count(&count).Error; err != nil {
		return 0, fmt.Errorf("failed to count speaker mappings for job %q: %w", jobID, err)
	}
	return int(count), nil
}

// BatchReconcileResult reports the outcome of one bounded ReconcileBatch run.
// Like ReconcileResult, it carries counts and job identifiers only, never
// embedding data or the identity of a candidate that did not match.
type BatchReconcileResult struct {
	JobsConsidered      int
	ProcessedJobIDs     []string
	JobsMatched         int
	SkippedMissingAudio []string
	MappingsWritten     int
}

// ReconcileBatch selects up to limit completed, diarized jobs, oldest first,
// and calls ReconcileJob on each strictly sequentially: one job at a time,
// no goroutines. This is the queue-stampede guard for the CPU-bound
// embedding extractor, which is also shared with live transcription jobs; a
// caller wanting an entire library reconciled makes repeated bounded calls
// rather than triggering one unbounded or concurrent sweep.
func (reconciler *JobReconciler) ReconcileBatch(ctx context.Context, limit int) (BatchReconcileResult, error) {
	var candidateJobIDs []string
	if err := reconciler.db.WithContext(ctx).
		Model(&models.TranscriptionJob{}).
		Where("status = ? AND diarization = ?", models.StatusCompleted, true).
		Order("created_at ASC").
		Limit(limit).
		Pluck("id", &candidateJobIDs).Error; err != nil {
		return BatchReconcileResult{}, fmt.Errorf("failed to select batch reconciliation candidates: %w", err)
	}

	result := BatchReconcileResult{JobsConsidered: len(candidateJobIDs)}
	for _, jobID := range candidateJobIDs {
		jobResult, err := reconciler.ReconcileJob(ctx, jobID)
		if err != nil {
			return BatchReconcileResult{}, err
		}

		result.ProcessedJobIDs = append(result.ProcessedJobIDs, jobID)
		if jobResult.Skipped {
			result.SkippedMissingAudio = append(result.SkippedMissingAudio, jobID)
			continue
		}
		if jobResult.Matched {
			result.JobsMatched++
		}
		result.MappingsWritten += jobResult.MappingsWritten
	}

	logger.Info("Speaker batch reconciliation completed",
		"jobs_considered", result.JobsConsidered,
		"jobs_processed", len(result.ProcessedJobIDs),
		"jobs_skipped_missing_audio", len(result.SkippedMissingAudio),
		"mappings_written", result.MappingsWritten,
	)
	return result, nil
}
