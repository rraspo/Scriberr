package database

import (
	"testing"

	"scriberr/internal/models"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type legacySpeakerMapping struct {
	ID                 uint   `gorm:"primaryKey;autoIncrement"`
	TranscriptionJobID string `gorm:"type:varchar(36);not null;index"`
	OriginalSpeaker    string `gorm:"type:varchar(50);not null"`
	CustomName         string `gorm:"type:varchar(100);not null"`
}

func (legacySpeakerMapping) TableName() string {
	return "speaker_mappings"
}

func TestSpeakerMappingMigrationPreservesRowsAndDefaultsProvenance(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:speaker_mapping_migration?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)

	require.NoError(t, db.AutoMigrate(&legacySpeakerMapping{}))
	legacyRows := []legacySpeakerMapping{
		{TranscriptionJobID: "job-one", OriginalSpeaker: "SPEAKER_00", CustomName: "Ada"},
		{TranscriptionJobID: "job-one", OriginalSpeaker: "SPEAKER_01", CustomName: "Grace"},
		{TranscriptionJobID: "job-two", OriginalSpeaker: "SPEAKER_00", CustomName: "Edsger"},
	}
	require.NoError(t, db.Create(&legacyRows).Error)

	require.NoError(t, db.AutoMigrate(
		&models.TranscriptionJob{},
		&models.MultiTrackFile{},
		&models.SpeakerMapping{},
		&models.SpeakerProfile{},
		&models.SpeakerProfileSample{},
	))

	var migrated []models.SpeakerMapping
	require.NoError(t, db.Order("id asc").Find(&migrated).Error)
	require.Len(t, migrated, 3)
	require.Equal(t, "Ada", migrated[0].CustomName)
	require.Equal(t, "Grace", migrated[1].CustomName)
	require.Equal(t, "Edsger", migrated[2].CustomName)

	for _, mapping := range migrated {
		require.Equal(t, "manual", mapping.Source)
		require.Nil(t, mapping.SpeakerProfileID)
		require.Nil(t, mapping.Confidence)
	}

	migrator := db.Migrator()
	require.True(t, migrator.HasTable(&models.SpeakerProfile{}))
	require.True(t, migrator.HasTable(&models.SpeakerProfileSample{}))
	require.True(t, migrator.HasColumn(&models.SpeakerMapping{}, "speaker_profile_id"))
	require.True(t, migrator.HasColumn(&models.SpeakerMapping{}, "source"))
	require.True(t, migrator.HasColumn(&models.SpeakerMapping{}, "confidence"))
}
