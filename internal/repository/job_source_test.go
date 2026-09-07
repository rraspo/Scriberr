package repository

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"scriberr/internal/models"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func openJobSourceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.TranscriptionJob{}))
	return db
}

func TestCreateJobWithoutSourceDefaultsToManual(t *testing.T) {
	db := openJobSourceTestDB(t)
	repo := NewJobRepository(db)
	ctx := context.Background()

	job := &models.TranscriptionJob{ID: "job-without-source", AudioPath: "audio.wav"}
	require.NoError(t, repo.Create(ctx, job))

	found, err := repo.FindByID(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, "manual", found.Source)
}

func TestCreateJobWithSourceOmiPersists(t *testing.T) {
	db := openJobSourceTestDB(t)
	repo := NewJobRepository(db)
	ctx := context.Background()

	job := &models.TranscriptionJob{ID: "job-with-omi-source", AudioPath: "audio.wav", Source: "omi"}
	require.NoError(t, repo.Create(ctx, job))

	found, err := repo.FindByID(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, "omi", found.Source)
}

func TestListWithParamsFiltersBySource(t *testing.T) {
	db := openJobSourceTestDB(t)
	repo := NewJobRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &models.TranscriptionJob{ID: "manual-job", AudioPath: "a.wav", Source: "manual"}))
	require.NoError(t, repo.Create(ctx, &models.TranscriptionJob{ID: "omi-job", AudioPath: "b.wav", Source: "omi"}))

	tests := []struct {
		name         string
		sourceFilter string
		wantIDs      []string
	}{
		{name: "filters to omi jobs only", sourceFilter: "omi", wantIDs: []string{"omi-job"}},
		{name: "filters to manual jobs only", sourceFilter: "manual", wantIDs: []string{"manual-job"}},
		{name: "no filter returns every job", sourceFilter: "", wantIDs: []string{"manual-job", "omi-job"}},
		{name: "unknown source returns no jobs, not an error", sourceFilter: "nonexistent", wantIDs: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs, total, err := repo.ListWithParams(ctx, 0, 10, "", "", "", tt.sourceFilter, nil)
			require.NoError(t, err)
			require.Equal(t, int64(len(tt.wantIDs)), total)

			gotIDs := make([]string, 0, len(jobs))
			for _, job := range jobs {
				gotIDs = append(gotIDs, job.ID)
			}
			require.ElementsMatch(t, tt.wantIDs, gotIDs)
		})
	}
}
