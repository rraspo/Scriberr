package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"scriberr/internal/models"
	"scriberr/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// fakeVideoYtDlpScript stands in for yt-dlp when a link import downloads the
// video and keeps it (-k) beside the extracted mp3. FAKE_YTDLP_MODE "noaudio"
// keeps the video but fails the audio extraction.
const fakeVideoYtDlpScript = `#!/bin/sh
mode="${FAKE_YTDLP_MODE:-ok}"
if [ -n "$FAKE_YTDLP_LOG" ]; then echo "$*" >> "$FAKE_YTDLP_LOG"; fi
output=""
previous=""
for argument in "$@"; do
  if [ "$argument" = "--dump-json" ]; then
    printf '%s\n' '{"id":"ABC123xyz","title":"Video by creator.handle","description":"A visual reel","channel":"creator.handle","uploader":"Creator Name","duration":30,"language":"en"}'
    exit 0
  fi
  if [ "$previous" = "--output" ] || [ "$previous" = "-o" ]; then
    output="$argument"
  fi
  previous="$argument"
done
video=$(echo "$output" | sed 's/%(ext)s/mp4/')
printf 'fakevideo' > "$video"
if [ "$mode" = "noaudio" ]; then
  echo "ERROR: Postprocessing: WARNING: unable to obtain file audio codec with ffprobe" >&2
  exit 1
fi
printf 'ID3fake' > "$(echo "$output" | sed 's/%(ext)s/mp3/')"
exit 0
`

// fakeFrameFfmpegScript stands in for ffmpeg in visuals extraction:
//   - a pass with "showinfo" prints scene changes at FAKE_SCENES (seconds);
//   - a pass whose output is a numbered pattern (%05d) copies FAKE_JPEG to
//     FAKE_FRAME_COUNT files numbered from 1;
//   - any other call copies FAKE_JPEG to its last argument.
//
// FAKE_FFMPEG_FAIL makes every call fail.
const fakeFrameFfmpegScript = `#!/bin/sh
if [ -n "$FAKE_FFMPEG_LOG" ]; then echo "$*" >> "$FAKE_FFMPEG_LOG"; fi
if [ -n "$FAKE_FFMPEG_FAIL" ]; then echo "ffmpeg broke" >&2; exit 1; fi
for argument in "$@"; do output="$argument"; done
case "$*" in
  *showinfo*)
    for seconds in $FAKE_SCENES; do
      echo "[Parsed_showinfo_1 @ 0x0] n:   1 pts: 100 pts_time:$seconds duration: 1 fmt:yuv420p" >&2
    done
    exit 0 ;;
esac
case "$output" in
  *%05d*)
    number=1
    while [ "$number" -le "${FAKE_FRAME_COUNT:-0}" ]; do
      cp "$FAKE_JPEG" "$(printf "$output" "$number")"
      number=$((number + 1))
    done
    exit 0 ;;
esac
cp "$FAKE_JPEG" "$output"
exit 0
`

// fakeFfprobeScript reports FAKE_DURATION as the media duration and a
// 1080x1920 video stream.
const fakeFfprobeScript = `#!/bin/sh
case "$*" in
  *width*) echo "1080,1920" ;;
  *) echo "${FAKE_DURATION:-30}" ;;
esac
`

// writeTestJPEG writes a real 405x720 JPEG (the size extracted frames have)
// for the fakes to copy, so sheet building decodes genuine images.
func writeTestJPEG(t *testing.T, path string) {
	t.Helper()
	frame := image.NewRGBA(image.Rect(0, 0, 405, 720))
	for y := 0; y < 720; y++ {
		for x := 0; x < 405; x++ {
			frame.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var encoded bytes.Buffer
	require.NoError(t, jpeg.Encode(&encoded, frame, nil))
	require.NoError(t, os.WriteFile(path, encoded.Bytes(), 0o644))
}

func installFakeVisualTools(t *testing.T, ytDlpMode string, durationSeconds int) (ytDlpLog string, ffmpegLog string) {
	t.Helper()
	binDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "yt-dlp"), []byte(fakeVideoYtDlpScript), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "ffmpeg"), []byte(fakeFrameFfmpegScript), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "ffprobe"), []byte(fakeFfprobeScript), 0o755))
	fixture := filepath.Join(binDir, "frame.jpg")
	writeTestJPEG(t, fixture)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_YTDLP_MODE", ytDlpMode)
	ytDlpLog = filepath.Join(binDir, "ytdlp.log")
	ffmpegLog = filepath.Join(binDir, "ffmpeg.log")
	t.Setenv("FAKE_YTDLP_LOG", ytDlpLog)
	t.Setenv("FAKE_FFMPEG_LOG", ffmpegLog)
	t.Setenv("FAKE_JPEG", fixture)
	t.Setenv("FAKE_DURATION", fmt.Sprint(durationSeconds))
	t.Setenv("FAKE_FRAME_COUNT", fmt.Sprint(int(frameRateFor(float64(durationSeconds))*float64(durationSeconds))))
	t.Setenv("FAKE_SCENES", "")
	return ytDlpLog, ffmpegLog
}

func performVisualsRequest(handler *Handler, method string, path string, params gin.Params, handle func(*gin.Context)) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(method, path, nil)
	ginContext.Params = params
	handle(ginContext)
	return recorder
}

func getVisuals(handler *Handler, jobID string) *httptest.ResponseRecorder {
	return performVisualsRequest(handler, http.MethodGet, "/api/v1/transcription/"+jobID+"/visuals", gin.Params{{Key: "id", Value: jobID}}, handler.GetJobVisuals)
}

type visualsBody struct {
	Status string            `json:"status"`
	Error  *string           `json:"error"`
	Sheets []models.JobSheet `json:"sheets"`
	Frames []models.JobFrame `json:"frames"`
	Video  *models.JobVideo  `json:"video"`
}

func decodeVisuals(t *testing.T, recorder *httptest.ResponseRecorder) visualsBody {
	t.Helper()
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var body visualsBody
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	return body
}

// waitForVisuals polls the visuals API until extraction leaves "pending".
func waitForVisuals(t *testing.T, handler *Handler, jobID string) visualsBody {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		body := decodeVisuals(t, getVisuals(handler, jobID))
		if body.Status != "pending" || time.Now().After(deadline) {
			return body
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestFrameRateCoversTheWholeVideo(t *testing.T) {
	require.Equal(t, 2.0, frameRateFor(15), "under 20 s: two frames a second")
	require.Equal(t, 1.0, frameRateFor(20))
	require.Equal(t, 1.0, frameRateFor(600), "up to ten minutes: one a second")
	require.InDelta(t, 600.0/1800.0, frameRateFor(1800), 1e-9, "longer: about 600 frames in all")
}

func TestFrameTimesAreEvenlySpacedFromZero(t *testing.T) {
	require.Equal(t, []float64{0, 0.5, 1, 1.5, 2, 2.5}, frameTimes(3, nil))
	times := frameTimes(60, nil)
	require.Len(t, times, 60)
	require.Equal(t, 59.0, times[59])
}

func TestLongVideosAddEveryDistinctSceneChange(t *testing.T) {
	times := frameTimes(1800, []float64{100.2, 301, 301.4, 1000})
	require.Contains(t, times, 100.2)
	require.Contains(t, times, 1000.0)
	require.NotContains(t, times, 301.4, "a scene change next to one already kept adds nothing")
	for index := 1; index < len(times); index++ {
		require.Greater(t, times[index], times[index-1])
	}
	require.LessOrEqual(t, len(times), 604)
	require.Equal(t, frameTimes(300, []float64{100.2}), frameTimes(300, nil), "short videos ignore scene changes: every second is already there")
}

func TestFormatSheetTimestamp(t *testing.T) {
	require.Equal(t, "0:00", formatSheetTimestamp(0))
	require.Equal(t, "0:07.5", formatSheetTimestamp(7.5))
	require.Equal(t, "1:05", formatSheetTimestamp(65))
	require.Equal(t, "1:00:01", formatSheetTimestamp(3601))
}

func TestBuildContactSheetsTilesTwelveFramesInOrder(t *testing.T) {
	directory := t.TempDir()
	frames := make([]models.JobFrame, 0, 30)
	for index := 0; index < 30; index++ {
		path := filepath.Join(directory, fmt.Sprintf("%d.jpg", index))
		writeTestJPEG(t, path)
		frames = append(frames, models.JobFrame{Index: index, TimestampSeconds: float64(index)})
	}
	sheetDirectory := filepath.Join(directory, "sheets")

	sheets, err := buildContactSheets(directory, sheetDirectory, frames, "job-1")
	require.NoError(t, err)
	require.Len(t, sheets, 3)
	require.Equal(t, 12, sheets[0].FrameCount)
	require.Equal(t, 0.0, sheets[0].StartSeconds)
	require.Equal(t, 11.0, sheets[0].EndSeconds)
	require.Equal(t, 6, sheets[2].FrameCount)
	require.Equal(t, 24.0, sheets[2].StartSeconds)
	require.Equal(t, 29.0, sheets[2].EndSeconds)
	require.Equal(t, "/api/v1/transcription/job-1/sheets/2", sheets[2].URL)
	require.Equal(t, "image/jpeg", sheets[2].ContentType)

	file, err := os.Open(filepath.Join(sheetDirectory, "0.jpg"))
	require.NoError(t, err)
	defer file.Close()
	config, err := jpeg.DecodeConfig(file)
	require.NoError(t, err)
	require.Equal(t, 1600, config.Width, "four 400 px tiles across")
	require.Equal(t, 3*711, config.Height, "three rows of 9:16 tiles")
}

func TestLinkImportKeepsTheVideoAndBuildsVisuals(t *testing.T) {
	ytDlpLog, _ := installFakeVisualTools(t, "ok", 30)
	handler, db, uploadDir := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)

	response := performLinkImport(t, handler, map[string]any{"url": "https://www.instagram.com/reel/ABC123xyz/"})
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	created := decodeLinkImport(t, response)

	calls := readCalls(t, ytDlpLog)
	download := calls[len(calls)-1]
	require.Contains(t, download, " -k ", "the video is kept beside the extracted audio")
	require.Contains(t, download, "height<=720", "video is fetched at most at 720p")

	visuals := waitForVisuals(t, handler, created.JobID)
	require.Equal(t, "ready", visuals.Status)
	require.Nil(t, visuals.Error)
	require.Len(t, visuals.Frames, 30, "one frame a second for 30 s")
	require.Equal(t, 0.0, visuals.Frames[0].TimestampSeconds)
	require.Equal(t, 29.0, visuals.Frames[29].TimestampSeconds)
	require.Equal(t, "/api/v1/transcription/"+created.JobID+"/frames/29", visuals.Frames[29].URL)
	require.Len(t, visuals.Sheets, 3)
	require.NotNil(t, visuals.Video)
	require.Equal(t, "/api/v1/transcription/"+created.JobID+"/video", visuals.Video.URL)
	require.Equal(t, "video/mp4", visuals.Video.ContentType)
	require.Equal(t, int64(len("fakevideo")), visuals.Video.SizeBytes)
	require.Equal(t, 30.0, visuals.Video.DurationSeconds)

	var stored models.TranscriptionJob
	require.NoError(t, db.First(&stored, "id = ?", created.JobID).Error)
	require.Equal(t, ".mp3", filepath.Ext(stored.AudioPath), "the audio, not the video, is transcribed")
	require.NotNil(t, stored.VideoPath)
	require.Equal(t, filepath.Join(uploadDir, created.JobID+".mp4"), *stored.VideoPath)
	require.FileExists(t, filepath.Join(uploadDir, "visuals", created.JobID, "frames", "29.jpg"))
	require.FileExists(t, filepath.Join(uploadDir, "visuals", created.JobID, "sheets", "2.jpg"))

	status := performLinkStatus(handler, created.JobID)
	require.Contains(t, status.Body.String(), `"visuals_status":"ready"`)
	require.Contains(t, status.Body.String(), `"visuals_url":"/api/v1/transcription/`+created.JobID+`/visuals"`)
}

func TestVisualsFilesAreServed(t *testing.T) {
	installFakeVisualTools(t, "ok", 15)
	handler, db, _ := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)
	created := decodeLinkImport(t, performLinkImport(t, handler, map[string]any{"url": "https://www.instagram.com/reel/ABC123xyz/"}))
	visuals := waitForVisuals(t, handler, created.JobID)
	require.Len(t, visuals.Frames, 30, "15 s at two frames a second")

	params := func(index string) gin.Params {
		return gin.Params{{Key: "id", Value: created.JobID}, {Key: "index", Value: index}}
	}
	sheet := performVisualsRequest(handler, http.MethodGet, "/sheets/0", params("0"), handler.GetJobSheet)
	require.Equal(t, http.StatusOK, sheet.Code)
	require.Equal(t, "image/jpeg", sheet.Header().Get("Content-Type"))
	frame := performVisualsRequest(handler, http.MethodGet, "/frames/29", params("29"), handler.GetJobFrame)
	require.Equal(t, http.StatusOK, frame.Code)
	require.Equal(t, "image/jpeg", frame.Header().Get("Content-Type"))
	for _, index := range []string{"30", "-1", "x", "../0"} {
		require.Equal(t, http.StatusNotFound, performVisualsRequest(handler, http.MethodGet, "/frames/"+index, params(index), handler.GetJobFrame).Code, index)
		require.Equal(t, http.StatusNotFound, performVisualsRequest(handler, http.MethodGet, "/sheets/"+index, params(index), handler.GetJobSheet).Code, index)
	}
	video := performVisualsRequest(handler, http.MethodGet, "/video", gin.Params{{Key: "id", Value: created.JobID}}, handler.GetJobVideo)
	require.Equal(t, http.StatusOK, video.Code)
	require.Equal(t, "video/mp4", video.Header().Get("Content-Type"))
	require.Equal(t, "fakevideo", video.Body.String())
}

func TestAVideoOverTheCapIsDroppedAfterItsVisualsAreBuilt(t *testing.T) {
	installFakeVisualTools(t, "ok", 30)
	handler, db, uploadDir := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)
	previous := maxKeptVideoBytes
	maxKeptVideoBytes = 3
	t.Cleanup(func() { maxKeptVideoBytes = previous })

	created := decodeLinkImport(t, performLinkImport(t, handler, map[string]any{"url": "https://www.instagram.com/reel/ABC123xyz/"}))
	visuals := waitForVisuals(t, handler, created.JobID)
	require.Equal(t, "ready", visuals.Status)
	require.Len(t, visuals.Frames, 30)
	require.Nil(t, visuals.Video)
	require.NoFileExists(t, filepath.Join(uploadDir, created.JobID+".mp4"))
	var stored models.TranscriptionJob
	require.NoError(t, db.First(&stored, "id = ?", created.JobID).Error)
	require.Nil(t, stored.VideoPath)
}

func TestVisualsOfAReelWithoutAudioAreStillBuilt(t *testing.T) {
	installFakeVisualTools(t, "noaudio", 12)
	handler, db, _ := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)

	created := decodeLinkImport(t, performLinkImport(t, handler, map[string]any{"url": "https://www.instagram.com/reel/ABC123xyz/"}))
	require.Equal(t, "failed", created.Status)
	visuals := waitForVisuals(t, handler, created.JobID)
	require.Equal(t, "ready", visuals.Status)
	require.Len(t, visuals.Frames, 24)
}

func TestAVisualsFailureNeverFailsTheImport(t *testing.T) {
	installFakeVisualTools(t, "ok", 30)
	t.Setenv("FAKE_FFMPEG_FAIL", "1")
	handler, db, _ := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)

	created := decodeLinkImport(t, performLinkImport(t, handler, map[string]any{"url": "https://www.instagram.com/reel/ABC123xyz/"}))
	require.Equal(t, "pending", created.Status)
	visuals := waitForVisuals(t, handler, created.JobID)
	require.Equal(t, "failed", visuals.Status)
	require.NotNil(t, visuals.Error)
	require.Empty(t, visuals.Sheets)
}

func TestVisualsForJobsWithoutThem(t *testing.T) {
	handler, db, _ := newYouTubeTestHandler(t)
	require.NoError(t, db.Create(&models.TranscriptionJob{ID: "older-job", AudioPath: "/data/uploads/a.mp3"}).Error)

	recorder := getVisuals(handler, "older-job")
	body := decodeVisuals(t, recorder)
	require.Equal(t, "none", body.Status)
	require.Empty(t, body.Sheets)
	require.Nil(t, body.Video)
	require.Contains(t, recorder.Body.String(), `"error":null`)
	require.Equal(t, http.StatusNotFound, getVisuals(handler, "missing-job").Code)
}

func TestDeletingAJobRemovesItsVisualsAndVideo(t *testing.T) {
	installFakeVisualTools(t, "ok", 15)
	handler, db, uploadDir := newYouTubeTestHandler(t)
	handler.fileService = service.NewFileService()
	createHouseProfiles(t, db)
	created := decodeLinkImport(t, performLinkImport(t, handler, map[string]any{"url": "https://www.instagram.com/reel/ABC123xyz/"}))
	waitForVisuals(t, handler, created.JobID)
	require.DirExists(t, filepath.Join(uploadDir, "visuals", created.JobID))

	deleted := performVisualsRequest(handler, http.MethodDelete, "/api/v1/transcription/"+created.JobID, gin.Params{{Key: "id", Value: created.JobID}}, handler.DeleteTranscriptionJob)
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())
	require.NoDirExists(t, filepath.Join(uploadDir, "visuals", created.JobID))
	require.NoFileExists(t, filepath.Join(uploadDir, created.JobID+".mp4"))
}

// Jobs imported before visuals existed get them by downloading the video
// again from their source link; the transcript and audio are left alone.
func TestBackfillBuildsVisualsForAnEarlierImport(t *testing.T) {
	ytDlpLog, _ := installFakeVisualTools(t, "ok", 30)
	handler, db, uploadDir := newYouTubeTestHandler(t)
	sourceURL := "https://www.instagram.com/reel/ABC123xyz/"
	transcript := `{"text":"kept"}`
	audioPath := filepath.Join(uploadDir, "earlier-job.mp3")
	require.NoError(t, os.WriteFile(audioPath, []byte("original audio"), 0o644))
	job := models.TranscriptionJob{ID: "earlier-job", AudioPath: audioPath, Source: "instagram", SourceURL: &sourceURL, Status: models.StatusCompleted, Transcript: &transcript}
	require.NoError(t, db.Create(&job).Error)

	accepted := performVisualsRequest(handler, http.MethodPost, "/api/v1/transcription/earlier-job/visuals", gin.Params{{Key: "id", Value: "earlier-job"}}, handler.BuildJobVisuals)
	require.Equal(t, http.StatusAccepted, accepted.Code, accepted.Body.String())
	visuals := waitForVisuals(t, handler, "earlier-job")
	require.Equal(t, "ready", visuals.Status)
	require.Len(t, visuals.Frames, 30)
	require.NotNil(t, visuals.Video)
	require.Contains(t, readCalls(t, ytDlpLog)[0], sourceURL)

	var stored models.TranscriptionJob
	require.NoError(t, db.First(&stored, "id = ?", "earlier-job").Error)
	require.Equal(t, models.StatusCompleted, stored.Status)
	require.Equal(t, transcript, *stored.Transcript)
	require.Equal(t, audioPath, stored.AudioPath)
	original, err := os.ReadFile(audioPath)
	require.NoError(t, err)
	require.Equal(t, "original audio", string(original), "the transcribed audio is untouched")
}

func TestBackfillNeedsASupportedSourceLink(t *testing.T) {
	installFakeVisualTools(t, "ok", 30)
	handler, db, _ := newYouTubeTestHandler(t)
	require.NoError(t, db.Create(&models.TranscriptionJob{ID: "upload-job", AudioPath: "/data/uploads/u.mp3"}).Error)

	response := performVisualsRequest(handler, http.MethodPost, "/visuals", gin.Params{{Key: "id", Value: "upload-job"}}, handler.BuildJobVisuals)
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Equal(t, "no_source_link", decodeYouTubeError(t, response).Code)
	missing := performVisualsRequest(handler, http.MethodPost, "/visuals", gin.Params{{Key: "id", Value: "missing-job"}}, handler.BuildJobVisuals)
	require.Equal(t, http.StatusNotFound, missing.Code)
}
