package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A container can run longer than its video stream (here 61.42 s against
// 61.40 s), and ffmpeg's fps filter rounds to the nearest frame, so a
// one-a-second grid yields 61 frames where the duration predicts 62. The
// frames that were extracted still make the visuals.
func TestAShortFallOfGridFramesStillBuildsTheVisuals(t *testing.T) {
	installFakeVisualTools(t, "ok", 61)
	t.Setenv("FAKE_DURATION", "61.42")
	t.Setenv("FAKE_FRAME_COUNT", "61")
	handler, db, uploadDir := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)

	created := decodeLinkImport(t, performLinkImport(t, handler, map[string]any{"url": "https://www.instagram.com/reel/ABC123xyz/"}))
	visuals := waitForVisuals(t, handler, created.JobID)

	require.Equal(t, "ready", visuals.Status, "error: %v", visuals.Error)
	require.Nil(t, visuals.Error)
	require.Len(t, visuals.Frames, 61)
	require.Equal(t, 60.0, visuals.Frames[60].TimestampSeconds)
	require.Len(t, visuals.Sheets, 6)
	require.FileExists(t, filepath.Join(uploadDir, "visuals", created.JobID, "frames", "60.jpg"))
	require.NoFileExists(t, filepath.Join(uploadDir, "visuals", created.JobID, "frames", "61.jpg"))
}

// Over ten minutes, scene-change frames sit between grid frames; a grid that
// comes up short keeps every scene frame, and the frames stay numbered in
// time order.
func TestAShortFallOnALongVideoKeepsSceneFramesInOrder(t *testing.T) {
	installFakeVisualTools(t, "ok", 1200)
	t.Setenv("FAKE_DURATION", "1200.4")
	expected := len(frameTimes(1200.4, nil))
	t.Setenv("FAKE_FRAME_COUNT", "599")
	t.Setenv("FAKE_SCENES", "100.9 1199.9")
	handler, db, _ := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)

	created := decodeLinkImport(t, performLinkImport(t, handler, map[string]any{"url": "https://www.instagram.com/reel/ABC123xyz/"}))
	visuals := waitForVisuals(t, handler, created.JobID)

	require.Equal(t, "ready", visuals.Status, "error: %v", visuals.Error)
	require.Equal(t, 600, expected, "the grid predicts 600 frames")
	require.Len(t, visuals.Frames, 601, "599 grid frames plus both scene changes, which are extracted on their own")
	for index, frame := range visuals.Frames {
		require.Equal(t, index, frame.Index)
		if index > 0 {
			require.Greater(t, frame.TimestampSeconds, visuals.Frames[index-1].TimestampSeconds)
		}
	}
}

// When ffmpeg extracts nothing at all, the build still fails with a reason.
func TestNoExtractedFramesFailsTheVisuals(t *testing.T) {
	installFakeVisualTools(t, "ok", 30)
	t.Setenv("FAKE_FRAME_COUNT", "0")
	handler, db, uploadDir := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)

	created := decodeLinkImport(t, performLinkImport(t, handler, map[string]any{"url": "https://www.instagram.com/reel/ABC123xyz/"}))
	visuals := waitForVisuals(t, handler, created.JobID)

	require.Equal(t, "failed", visuals.Status)
	require.NotNil(t, visuals.Error)
	require.Empty(t, visuals.Frames)
	require.Empty(t, visuals.Sheets)
	require.NoDirExists(t, filepath.Join(uploadDir, "visuals", created.JobID))
}

// yt-dlp's -k keeps the separate streams it merged into the mp4; they are
// removed after the download so only the video and the mp3 stay.
func TestMergedDownloadLeavesOnlyTheVideoAndTheAudio(t *testing.T) {
	installFakeVisualTools(t, "merged", 30)
	handler, db, uploadDir := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)

	response := performLinkImport(t, handler, map[string]any{"url": "https://www.instagram.com/reel/ABC123xyz/"})
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	created := decodeLinkImport(t, response)
	waitForVisuals(t, handler, created.JobID)

	entries, err := os.ReadDir(uploadDir)
	require.NoError(t, err)
	var jobFiles []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), created.JobID) {
			jobFiles = append(jobFiles, entry.Name())
		}
	}
	require.ElementsMatch(t, []string{created.JobID + ".mp3", created.JobID + ".mp4"}, jobFiles)
}
