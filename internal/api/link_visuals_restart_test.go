package api

import (
	"testing"

	"scriberr/internal/models"

	"github.com/stretchr/testify/require"
)

// Visuals are built in the background, so a restart can cut one short. On
// start, such a build is marked failed with the reason and how to retry,
// instead of staying "pending" forever.
func TestInterruptedVisualsAreMarkedFailedOnStart(t *testing.T) {
	_, db, _ := newYouTubeTestHandler(t)
	require.NoError(t, db.Create(&models.TranscriptionJob{ID: "cut-short", AudioPath: "a.mp3", VisualsStatus: models.VisualsPending}).Error)
	require.NoError(t, db.Create(&models.TranscriptionJob{ID: "finished", AudioPath: "b.mp3", VisualsStatus: models.VisualsReady}).Error)

	require.NoError(t, FailInterruptedVisuals(db))

	var cut models.TranscriptionJob
	require.NoError(t, db.First(&cut, "id = ?", "cut-short").Error)
	require.Equal(t, models.VisualsFailed, cut.VisualsStatus)
	require.NotNil(t, cut.VisualsError)
	require.Contains(t, *cut.VisualsError, "restart")
	var finished models.TranscriptionJob
	require.NoError(t, db.First(&finished, "id = ?", "finished").Error)
	require.Equal(t, models.VisualsReady, finished.VisualsStatus)
}
