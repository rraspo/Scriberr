package repository

import (
	"context"
	"sort"
	"testing"

	"scriberr/internal/models"

	"github.com/stretchr/testify/require"
)

// The list's source filter offers "upload" for everything that did not come
// from a named intake: manual uploads (including phone calls and memos),
// video uploads and any source the filter has no option for.
func TestListWithParamsUploadFilterCoversEverythingWithoutANamedIntake(t *testing.T) {
	db := openJobSourceTestDB(t)
	repo := NewJobRepository(db)
	ctx := context.Background()
	for _, source := range []string{"manual", "video", "zoom", "youtube", "instagram", "tiktok", "whatsapp", "plaud", "omi"} {
		require.NoError(t, repo.Create(ctx, &models.TranscriptionJob{ID: source + "-job", AudioPath: source + ".wav", Source: source}))
	}

	jobs, total, err := repo.ListWithParams(ctx, 0, 20, "", "", "", "upload", nil)
	require.NoError(t, err)
	ids := make([]string, 0, len(jobs))
	for _, job := range jobs {
		ids = append(ids, job.ID)
	}
	sort.Strings(ids)
	require.Equal(t, []string{"manual-job", "video-job", "zoom-job"}, ids)
	require.Equal(t, int64(3), total)

	instagram, _, err := repo.ListWithParams(ctx, 0, 20, "", "", "", "instagram", nil)
	require.NoError(t, err)
	require.Len(t, instagram, 1)
}
