package repository

import (
	"context"

	"scriberr/internal/models"

	"gorm.io/gorm"
)

// SpeakerProfileWithSampleCount pairs a speaker profile with how many
// samples have been enrolled against it.
type SpeakerProfileWithSampleCount struct {
	models.SpeakerProfile
	SampleCount int64
}

// SpeakerProfileRepository handles speaker profile and speaker profile
// sample database operations.
type SpeakerProfileRepository interface {
	Create(ctx context.Context, profile *models.SpeakerProfile) error
	Update(ctx context.Context, profile *models.SpeakerProfile) error
	FindByID(ctx context.Context, id uint) (*models.SpeakerProfile, error)
	FindByName(ctx context.Context, name string) (*models.SpeakerProfile, error)
	ListWithSampleCounts(ctx context.Context) ([]SpeakerProfileWithSampleCount, error)
	DeleteWithSamples(ctx context.Context, id uint) error
	CountSamples(ctx context.Context, profileID uint) (int64, error)
	ListSamples(ctx context.Context, profileID uint) ([]models.SpeakerProfileSample, error)
	DeleteSample(ctx context.Context, profileID, sampleID uint) error
}

type speakerProfileRepository struct {
	db *gorm.DB
}

// NewSpeakerProfileRepository creates a new speaker profile repository.
func NewSpeakerProfileRepository(db *gorm.DB) SpeakerProfileRepository {
	return &speakerProfileRepository{db: db}
}

func (r *speakerProfileRepository) Create(ctx context.Context, profile *models.SpeakerProfile) error {
	return r.db.WithContext(ctx).Create(profile).Error
}

func (r *speakerProfileRepository) Update(ctx context.Context, profile *models.SpeakerProfile) error {
	return r.db.WithContext(ctx).Save(profile).Error
}

func (r *speakerProfileRepository) FindByID(ctx context.Context, id uint) (*models.SpeakerProfile, error) {
	var profile models.SpeakerProfile
	err := r.db.WithContext(ctx).First(&profile, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &profile, nil
}

func (r *speakerProfileRepository) FindByName(ctx context.Context, name string) (*models.SpeakerProfile, error) {
	var profile models.SpeakerProfile
	err := r.db.WithContext(ctx).Where("name = ?", name).First(&profile).Error
	if err != nil {
		return nil, err
	}
	return &profile, nil
}

func (r *speakerProfileRepository) ListWithSampleCounts(ctx context.Context) ([]SpeakerProfileWithSampleCount, error) {
	var profiles []models.SpeakerProfile
	if err := r.db.WithContext(ctx).Order("name asc").Find(&profiles).Error; err != nil {
		return nil, err
	}

	type sampleCountRow struct {
		SpeakerProfileID uint
		Count            int64
	}
	var counts []sampleCountRow
	if err := r.db.WithContext(ctx).Model(&models.SpeakerProfileSample{}).
		Select("speaker_profile_id, COUNT(*) as count").
		Group("speaker_profile_id").
		Scan(&counts).Error; err != nil {
		return nil, err
	}

	countByProfileID := make(map[uint]int64, len(counts))
	for _, row := range counts {
		countByProfileID[row.SpeakerProfileID] = row.Count
	}

	results := make([]SpeakerProfileWithSampleCount, len(profiles))
	for i, profile := range profiles {
		results[i] = SpeakerProfileWithSampleCount{
			SpeakerProfile: profile,
			SampleCount:    countByProfileID[profile.ID],
		}
	}
	return results, nil
}

// DeleteWithSamples removes a speaker profile and every sample enrolled
// against it in a single transaction.
func (r *speakerProfileRepository) DeleteWithSamples(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("speaker_profile_id = ?", id).Delete(&models.SpeakerProfileSample{}).Error; err != nil {
			return err
		}
		return tx.Delete(&models.SpeakerProfile{}, "id = ?", id).Error
	})
}

func (r *speakerProfileRepository) CountSamples(ctx context.Context, profileID uint) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&models.SpeakerProfileSample{}).
		Where("speaker_profile_id = ?", profileID).Count(&count).Error
	return count, err
}

func (r *speakerProfileRepository) ListSamples(ctx context.Context, profileID uint) ([]models.SpeakerProfileSample, error) {
	var samples []models.SpeakerProfileSample
	err := r.db.WithContext(ctx).Where("speaker_profile_id = ?", profileID).Order("created_at asc").Find(&samples).Error
	if err != nil {
		return nil, err
	}
	return samples, nil
}

// DeleteSample removes a single sample, scoped to the profile it must
// belong to so a sample cannot be deleted via the wrong profile's URL.
func (r *speakerProfileRepository) DeleteSample(ctx context.Context, profileID, sampleID uint) error {
	return r.db.WithContext(ctx).
		Where("id = ? AND speaker_profile_id = ?", sampleID, profileID).
		Delete(&models.SpeakerProfileSample{}).Error
}
