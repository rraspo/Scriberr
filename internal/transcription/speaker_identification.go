package transcription

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"strings"

	"gorm.io/gorm"

	"scriberr/internal/models"
	"scriberr/internal/transcription/interfaces"
	"scriberr/pkg/logger"
)

// Match acceptance is deliberately conservative: a diarized label is renamed
// only when one enrolled profile is both close enough in absolute terms and
// clearly ahead of the next best candidate. Anything short of that leaves the
// SPEAKER_NN label untouched, because a confident wrong name costs more than
// no name at all.
//
// speakerMatchThreshold is an empirically derived default rather than a
// universal constant: the usable value scales with how many confusable voices
// are enrolled, since enrolling a similar-sounding speaker turns absolute
// thresholding into relative ranking, which the margin rule then handles.
// Both values are package-level defaults wired in at construction time so a
// deployment can tune them without touching the matching logic.
const (
	// 0.50 produced a live false positive: an unenrolled speaker scored 0.57
	// against a single-sample profile and claimed a meeting's dominant voice.
	// Genuine same-speaker matches observed so far score 0.62 and above.
	speakerMatchThreshold = 0.60
	speakerMatchMargin    = 0.08
)

// autoSpeakerMappingSource marks a speaker mapping this identifier wrote, as
// opposed to one a human entered.
const autoSpeakerMappingSource = "auto"

// SpeakerIdentifier names diarized speakers by comparing each diarized label's
// voice embedding against the enrolled speaker profiles. It only ever adds
// speaker mappings: an existing mapping for a label is never touched, and a
// label that cannot be matched confidently keeps its diarized name.
type SpeakerIdentifier struct {
	db        *gorm.DB
	extractor interfaces.SpeakerEmbeddingExtractor
	threshold float64
	margin    float64
}

// NewSpeakerIdentifier creates a speaker identifier with explicit matching
// bounds. threshold is the minimum cosine similarity a match must reach;
// margin is how far the best candidate must sit ahead of the runner-up.
func NewSpeakerIdentifier(db *gorm.DB, extractor interfaces.SpeakerEmbeddingExtractor, threshold float64, margin float64) *SpeakerIdentifier {
	return &SpeakerIdentifier{
		db:        db,
		extractor: extractor,
		threshold: threshold,
		margin:    margin,
	}
}

// NewDefaultSpeakerIdentifier creates a speaker identifier with the package
// default matching bounds, for production wiring.
func NewDefaultSpeakerIdentifier(db *gorm.DB, extractor interfaces.SpeakerEmbeddingExtractor) *SpeakerIdentifier {
	return NewSpeakerIdentifier(db, extractor, speakerMatchThreshold, speakerMatchMargin)
}

// speakerProfileCentroid is one enrolled profile reduced to a single
// comparable vector: the normalized mean of its enrolled samples.
type speakerProfileCentroid struct {
	profileID uint
	name      string
	centroid  []float64
}

// labelSimilarity records the best similarity observed for one diarized label,
// for the summary log line. It carries no candidate identity: an unmatched
// label must not leak which profiles were compared against it.
type labelSimilarity struct {
	label      string
	similarity float64
}

// IdentifySpeakers compares every diarized speaker label in result against the
// enrolled speaker profiles and writes a speaker mapping for each label that
// matches one profile confidently. It is additive only: labels that do not
// clear both the threshold and the margin are left as they are, and a label
// that already has a mapping is never overwritten regardless of that mapping's
// source, so a human-entered name always wins.
//
// A returned error means identification could not complete; the transcription
// job it ran for is already complete and correct either way.
func (identifier *SpeakerIdentifier) IdentifySpeakers(ctx context.Context, job *models.TranscriptionJob, result *interfaces.TranscriptResult) error {
	if identifier == nil || identifier.db == nil || identifier.extractor == nil || job == nil || result == nil {
		return nil
	}

	// Cost guard: identification applies only to diarized results. A job with
	// no speaker labels is not a candidate at all, so it stays silent.
	labels, segmentsByLabel := collectDiarizedLabels(result)
	if len(labels) == 0 {
		return nil
	}

	candidates, err := identifier.loadProfileCentroids(ctx)
	if err != nil {
		return err
	}

	existingLabels, err := identifier.loadMappedLabels(ctx, job.ID)
	if err != nil {
		return err
	}

	matchesWritten := 0
	similarities := make([]labelSimilarity, 0, len(labels))
	var firstFailure error

	// Cost guard: with no enrolled sample to compare against, the extractor is
	// never invoked. Enablement is data driven - sample presence alone decides
	// whether matching runs.
	if len(candidates) > 0 {
		for _, label := range labels {
			// An existing mapping can never be replaced, so extracting an
			// embedding for that label could only produce a discarded result.
			if existingLabels[label] {
				continue
			}

			embeddingResult, err := identifier.extractor.ExtractSpeakerEmbedding(ctx, job.AudioPath, segmentsByLabel[label])
			if err != nil {
				if firstFailure == nil {
					firstFailure = fmt.Errorf("speaker embedding extraction failed for label %q: %w", label, err)
				}
				continue
			}
			// A nil embedding means the label fell under the extractor's audio
			// floor, which is a skip rather than a failure.
			if embeddingResult == nil || len(embeddingResult.Embedding) == 0 {
				continue
			}

			embedding, err := decodeEmbeddingLittleEndianFloat32(embeddingResult.Embedding)
			if err != nil {
				if firstFailure == nil {
					firstFailure = fmt.Errorf("speaker embedding decode failed for label %q: %w", label, err)
				}
				continue
			}

			best, runnerUp, hasRunnerUp := bestSpeakerCandidate(embedding, candidates)
			if best == nil {
				continue
			}
			similarities = append(similarities, labelSimilarity{label: label, similarity: best.similarity})

			// A single enrolled profile has no runner-up, so the margin rule is
			// satisfied by construction and the threshold alone decides.
			if best.similarity < identifier.threshold {
				continue
			}
			if hasRunnerUp && best.similarity-runnerUp < identifier.margin {
				continue
			}

			if err := identifier.writeAutoMapping(ctx, job.ID, label, best); err != nil {
				if firstFailure == nil {
					firstFailure = err
				}
				continue
			}
			matchesWritten++
		}
	}

	logger.Info("Speaker identification completed",
		"job_id", job.ID,
		"labels_examined", len(labels),
		"profiles_considered", len(candidates),
		"matches_written", matchesWritten,
		"best_similarity_per_label", formatLabelSimilarities(similarities),
	)

	return firstFailure
}

// speakerMatch is one profile's similarity against a single diarized label.
type speakerMatch struct {
	profileID  uint
	name       string
	similarity float64
}

// bestSpeakerCandidate returns the highest-scoring profile for an embedding,
// the runner-up score, and whether a runner-up existed at all. With a single
// enrolled profile there is no runner-up and the margin rule does not apply.
func bestSpeakerCandidate(embedding []float64, candidates []speakerProfileCentroid) (*speakerMatch, float64, bool) {
	var best *speakerMatch
	runnerUp := 0.0
	hasRunnerUp := false

	for _, candidate := range candidates {
		similarity, ok := cosineSimilarity(embedding, candidate.centroid)
		if !ok {
			continue
		}
		if best == nil || similarity > best.similarity {
			if best != nil {
				runnerUp = best.similarity
				hasRunnerUp = true
			}
			best = &speakerMatch{profileID: candidate.profileID, name: candidate.name, similarity: similarity}
			continue
		}
		if !hasRunnerUp || similarity > runnerUp {
			runnerUp = similarity
			hasRunnerUp = true
		}
	}

	return best, runnerUp, hasRunnerUp
}

// writeAutoMapping records a confident match as a new speaker mapping.
func (identifier *SpeakerIdentifier) writeAutoMapping(ctx context.Context, jobID, label string, match *speakerMatch) error {
	profileID := match.profileID
	confidence := match.similarity
	mapping := models.SpeakerMapping{
		TranscriptionJobID: jobID,
		OriginalSpeaker:    label,
		CustomName:         match.name,
		SpeakerProfileID:   &profileID,
		Source:             autoSpeakerMappingSource,
		Confidence:         &confidence,
	}
	if err := identifier.db.WithContext(ctx).Create(&mapping).Error; err != nil {
		return fmt.Errorf("failed to write auto speaker mapping for label %q: %w", label, err)
	}
	return nil
}

// loadProfileCentroids reduces every enrolled profile to one comparable
// vector. Samples are read directly rather than through the job they came
// from: an embedding is an independent entity, so a profile whose source job
// was deleted still matches. Profiles without a usable sample are omitted,
// which is what makes enrollment data alone gate identification.
func (identifier *SpeakerIdentifier) loadProfileCentroids(ctx context.Context) ([]speakerProfileCentroid, error) {
	var profiles []models.SpeakerProfile
	if err := identifier.db.WithContext(ctx).Order("id asc").Find(&profiles).Error; err != nil {
		return nil, fmt.Errorf("failed to load speaker profiles: %w", err)
	}
	if len(profiles) == 0 {
		return nil, nil
	}

	var samples []models.SpeakerProfileSample
	if err := identifier.db.WithContext(ctx).Order("id asc").Find(&samples).Error; err != nil {
		return nil, fmt.Errorf("failed to load speaker profile samples: %w", err)
	}

	vectorsByProfile := make(map[uint][][]float64, len(profiles))
	for _, sample := range samples {
		vector, err := decodeEmbeddingLittleEndianFloat32(sample.Embedding)
		if err != nil {
			continue
		}
		vectorsByProfile[sample.SpeakerProfileID] = append(vectorsByProfile[sample.SpeakerProfileID], vector)
	}

	centroids := make([]speakerProfileCentroid, 0, len(profiles))
	for _, profile := range profiles {
		centroid, err := embeddingCentroid(vectorsByProfile[profile.ID])
		if err != nil {
			continue
		}
		centroids = append(centroids, speakerProfileCentroid{
			profileID: profile.ID,
			name:      profile.Name,
			centroid:  centroid,
		})
	}

	return centroids, nil
}

// loadMappedLabels returns the diarized labels of a job that already carry a
// speaker mapping, whatever its source.
func (identifier *SpeakerIdentifier) loadMappedLabels(ctx context.Context, jobID string) (map[string]bool, error) {
	var mappings []models.SpeakerMapping
	if err := identifier.db.WithContext(ctx).Where("transcription_job_id = ?", jobID).Find(&mappings).Error; err != nil {
		return nil, fmt.Errorf("failed to load existing speaker mappings: %w", err)
	}

	mapped := make(map[string]bool, len(mappings))
	for _, mapping := range mappings {
		mapped[mapping.OriginalSpeaker] = true
	}
	return mapped, nil
}

// collectDiarizedLabels groups a transcript's segments by speaker label,
// preserving the order labels first appear so extraction runs once per
// distinct label with only that label's segments.
func collectDiarizedLabels(result *interfaces.TranscriptResult) ([]string, map[string][]interfaces.SpeakerSegment) {
	var labels []string
	segmentsByLabel := map[string][]interfaces.SpeakerSegment{}

	for _, segment := range result.Segments {
		if segment.Speaker == nil {
			continue
		}
		label := strings.TrimSpace(*segment.Speaker)
		if label == "" {
			continue
		}
		if _, seen := segmentsByLabel[label]; !seen {
			labels = append(labels, label)
		}
		segmentsByLabel[label] = append(segmentsByLabel[label], interfaces.SpeakerSegment{
			Speaker: label,
			Start:   segment.Start,
			End:     segment.End,
		})
	}

	return labels, segmentsByLabel
}

// formatLabelSimilarities renders the per-label best similarity for the
// summary log line. It carries scores only, never candidate identities.
func formatLabelSimilarities(similarities []labelSimilarity) string {
	parts := make([]string, 0, len(similarities))
	for _, entry := range similarities {
		parts = append(parts, fmt.Sprintf("%s=%.4f", entry.label, entry.similarity))
	}
	return strings.Join(parts, " ")
}

// decodeEmbeddingLittleEndianFloat32 decodes a stored embedding BLOB into a
// float64 vector. Embeddings are stored as little-endian float32, so a blob
// whose length is not a whole number of float32 values is not an embedding.
func decodeEmbeddingLittleEndianFloat32(blob []byte) ([]float64, error) {
	if len(blob) == 0 || len(blob)%4 != 0 {
		return nil, fmt.Errorf("embedding blob of %d bytes is not a whole number of float32 values", len(blob))
	}
	values := make([]float64, len(blob)/4)
	for index := range values {
		values[index] = float64(math.Float32frombits(binary.LittleEndian.Uint32(blob[index*4:])))
	}
	return values, nil
}

// embeddingCentroid reduces a profile's sample vectors to their mean, then
// normalizes it to unit length so it can be compared by cosine similarity.
// Vectors of a differing dimension are ignored rather than failing the
// profile: a profile enrolled across an embedding-model change keeps the
// samples that still line up.
func embeddingCentroid(vectors [][]float64) ([]float64, error) {
	if len(vectors) == 0 {
		return nil, fmt.Errorf("no embedding vectors to average")
	}

	dimensions := len(vectors[0])
	sum := make([]float64, dimensions)
	used := 0
	for _, vector := range vectors {
		if len(vector) != dimensions {
			continue
		}
		for index, value := range vector {
			sum[index] += value
		}
		used++
	}
	if used == 0 {
		return nil, fmt.Errorf("no embedding vectors of a consistent dimension")
	}

	for index := range sum {
		sum[index] /= float64(used)
	}
	return normalizeEmbedding(sum)
}

// normalizeEmbedding scales a vector to unit length.
func normalizeEmbedding(vector []float64) ([]float64, error) {
	magnitude := 0.0
	for _, value := range vector {
		magnitude += value * value
	}
	magnitude = math.Sqrt(magnitude)
	if magnitude == 0 || math.IsNaN(magnitude) || math.IsInf(magnitude, 0) {
		return nil, fmt.Errorf("embedding vector has no usable magnitude")
	}

	normalized := make([]float64, len(vector))
	for index, value := range vector {
		normalized[index] = value / magnitude
	}
	return normalized, nil
}

// cosineSimilarity returns the cosine similarity of two vectors, and whether
// it could be computed at all - mismatched dimensions or a zero-magnitude
// vector have no defined similarity.
func cosineSimilarity(left, right []float64) (float64, bool) {
	if len(left) == 0 || len(left) != len(right) {
		return 0, false
	}

	dotProduct := 0.0
	leftMagnitude := 0.0
	rightMagnitude := 0.0
	for index := range left {
		dotProduct += left[index] * right[index]
		leftMagnitude += left[index] * left[index]
		rightMagnitude += right[index] * right[index]
	}
	if leftMagnitude == 0 || rightMagnitude == 0 {
		return 0, false
	}

	return dotProduct / (math.Sqrt(leftMagnitude) * math.Sqrt(rightMagnitude)), true
}
