package database

import (
	"fmt"
	"strings"
	"testing"

	"scriberr/internal/models"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type legacyTranscriptionProfile struct {
	ID          string  `gorm:"primaryKey;type:varchar(36)"`
	Name        string  `gorm:"type:varchar(255);not null"`
	Model       string  `gorm:"type:varchar(50)"`
	Language    *string `gorm:"type:varchar(10)"`
	Diarize     bool    `gorm:"type:boolean"`
	MinSpeakers *int    `gorm:"type:int"`
	MaxSpeakers *int    `gorm:"type:int"`
}

type legacyProfileBytes struct {
	Model       string
	Language    string
	Diarize     string
	MinSpeakers string
	MaxSpeakers string
}

func (legacyTranscriptionProfile) TableName() string {
	return "transcription_profiles"
}

func openMigrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	return db
}

func TestTranscriptionProfileMigrationBackfillsLocalExecutionDefaults(t *testing.T) {
	db := openMigrationTestDB(t)
	require.NoError(t, db.AutoMigrate(&legacyTranscriptionProfile{}))
	require.NoError(t, db.Create(&legacyTranscriptionProfile{ID: "legacy", Name: "Legacy"}).Error)

	require.NoError(t, db.AutoMigrate(&models.TranscriptionProfile{}))

	var profile models.TranscriptionProfile
	require.NoError(t, db.First(&profile, "id = ?", "legacy").Error)
	require.Equal(t, "local", profile.ExecutionMode)
	require.Equal(t, 22, profile.RemotePort)
	require.Equal(t, 10, profile.RemoteConnectTimeoutSeconds)
	require.Empty(t, profile.RemoteCommandPrefix)
}

func TestTranscriptionProfileMigrationPreservesExistingFields(t *testing.T) {
	db := openMigrationTestDB(t)
	require.NoError(t, db.AutoMigrate(&legacyTranscriptionProfile{}))
	language, minSpeakers, maxSpeakers := "es", 2, 6
	legacy := legacyTranscriptionProfile{
		ID: "full-legacy", Name: "Full Legacy", Model: "large-v3", Language: &language,
		Diarize: true, MinSpeakers: &minSpeakers, MaxSpeakers: &maxSpeakers,
	}
	require.NoError(t, db.Create(&legacy).Error)

	const storedBytesQuery = `
		SELECT
			hex(CAST(model AS BLOB)) AS model,
			hex(CAST(language AS BLOB)) AS language,
			hex(CAST(diarize AS BLOB)) AS diarize,
			hex(CAST(min_speakers AS BLOB)) AS min_speakers,
			hex(CAST(max_speakers AS BLOB)) AS max_speakers
		FROM transcription_profiles WHERE id = ?`
	var before legacyProfileBytes
	require.NoError(t, db.Raw(storedBytesQuery, legacy.ID).Scan(&before).Error)
	require.NoError(t, db.AutoMigrate(&models.TranscriptionProfile{}))
	var after legacyProfileBytes
	require.NoError(t, db.Raw(storedBytesQuery, legacy.ID).Scan(&after).Error)

	require.Equal(t, before, after)
}
