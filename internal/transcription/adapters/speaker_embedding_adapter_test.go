package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"scriberr/internal/transcription/interfaces"

	"github.com/stretchr/testify/require"
)

// newTestSpeakerEmbeddingAdapter builds an adapter with the environment
// bootstrap short-circuited, so extraction logic can be exercised without a
// real uv/Python toolchain.
func newTestSpeakerEmbeddingAdapter(t *testing.T) *SpeakerEmbeddingAdapter {
	t.Helper()
	adapter := NewSpeakerEmbeddingAdapter(t.TempDir())
	adapter.prepareEnvironment = func(ctx context.Context) error { return nil }
	return adapter
}

func TestSpeakerEmbeddingAdapterCommandCarriesSegmentLimits(t *testing.T) {
	adapter := newTestSpeakerEmbeddingAdapter(t)
	var capturedArgs []string
	adapter.runCommand = func(ctx context.Context, args []string, env []string) ([]byte, error) {
		capturedArgs = args
		writeSpeakerEmbeddingOutputFixture(t, outputPathFromArgs(t, args), "SPEAKER_00", []float64{1.0, 2.0}, 12.5, 2)
		return []byte("embedded 1/1 speakers"), nil
	}

	_, err := adapter.ExtractSpeakerEmbedding(context.Background(), "/srv/audio/job.wav", []interfaces.SpeakerSegment{
		{Speaker: "SPEAKER_00", Start: 1.0, End: 9.5},
	})
	require.NoError(t, err)

	require.Contains(t, capturedArgs, "--max-segments")
	require.Contains(t, capturedArgs, strconv.Itoa(speakerEmbeddingMaxSegments))
	require.Contains(t, capturedArgs, "--max-seconds")

	foundMaxSeconds := false
	for i, arg := range capturedArgs {
		if arg == "--max-seconds" && i+1 < len(capturedArgs) {
			foundMaxSeconds = true
			require.Equal(t, "30.0", capturedArgs[i+1])
		}
	}
	require.True(t, foundMaxSeconds, "argv must carry --max-seconds with a value")
}

func TestSpeakerEmbeddingAdapterParsesDimensionsAndSeconds(t *testing.T) {
	adapter := newTestSpeakerEmbeddingAdapter(t)
	adapter.runCommand = func(ctx context.Context, args []string, env []string) ([]byte, error) {
		writeSpeakerEmbeddingOutputFixture(t, outputPathFromArgs(t, args), "SPEAKER_00", []float64{1.0, 2.0, 3.0}, 18.25, 2)
		return nil, nil
	}

	result, err := adapter.ExtractSpeakerEmbedding(context.Background(), "/srv/audio/job.wav", []interfaces.SpeakerSegment{
		{Speaker: "SPEAKER_00", Start: 1.0, End: 9.5},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 3, result.Dimensions)
	require.InDelta(t, 18.25, result.SecondsUsed, 0.0001)
	require.Equal(t, 2, result.SegmentsUsed)

	expected := make([]byte, 12)
	for i, value := range []float64{1.0, 2.0, 3.0} {
		bits := math.Float32bits(float32(value))
		expected[i*4] = byte(bits)
		expected[i*4+1] = byte(bits >> 8)
		expected[i*4+2] = byte(bits >> 16)
		expected[i*4+3] = byte(bits >> 24)
	}
	require.Equal(t, expected, result.Embedding)
}

func TestSpeakerEmbeddingAdapterNullEmbeddingYieldsNilNotError(t *testing.T) {
	adapter := newTestSpeakerEmbeddingAdapter(t)
	adapter.runCommand = func(ctx context.Context, args []string, env []string) ([]byte, error) {
		writeSpeakerEmbeddingOutputFixtureRaw(t, outputPathFromArgs(t, args),
			`{"dimensions":0,"speakers":[{"speaker":"SPEAKER_00","embedding":null,"seconds_used":1.2,"segments_used":1}]}`)
		return nil, nil
	}

	result, err := adapter.ExtractSpeakerEmbedding(context.Background(), "/srv/audio/job.wav", []interfaces.SpeakerSegment{
		{Speaker: "SPEAKER_00", Start: 1.0, End: 2.2},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Nil(t, result.Embedding)
}

func TestSpeakerEmbeddingAdapterNonZeroExitSurfacesAsError(t *testing.T) {
	adapter := newTestSpeakerEmbeddingAdapter(t)
	adapter.runCommand = func(ctx context.Context, args []string, env []string) ([]byte, error) {
		// Deliberately does not write an output file: a failed process must
		// not be papered over with a zero-value result.
		return []byte("traceback: boom"), errors.New("exit status 1")
	}

	result, err := adapter.ExtractSpeakerEmbedding(context.Background(), "/srv/audio/job.wav", []interfaces.SpeakerSegment{
		{Speaker: "SPEAKER_00", Start: 1.0, End: 9.5},
	})
	require.Error(t, err)
	require.Nil(t, result)
}

func TestSpeakerEmbeddingAdapterLoggedCommandLineOmitsAudioPathAndVector(t *testing.T) {
	adapter := newTestSpeakerEmbeddingAdapter(t)
	audioPath := "/srv/audio/very-specific-job-id/job.wav"
	args := adapter.buildArgs(audioPath, "/tmp/x/segments.json", "/tmp/x/output.json")

	logged := RedactedCommand(args)

	require.NotContains(t, logged, audioPath)
	require.NotContains(t, logged, "/srv/audio")
	require.Contains(t, logged, "--audio ***")
	// The embedding vector is only ever written to the output file by the
	// Python script, never passed as a command argument, so RedactedCommand
	// covering the audio path is enough to keep this log line vector-free.
}

// outputPathFromArgs extracts the --output flag's value from a built argv,
// so fakes can write their fixture to the exact path the adapter will read.
func outputPathFromArgs(t *testing.T, args []string) string {
	t.Helper()
	for i, arg := range args {
		if arg == "--output" && i+1 < len(args) {
			return args[i+1]
		}
	}
	t.Fatalf("no --output flag found in args: %v", args)
	return ""
}

func writeSpeakerEmbeddingOutputFixture(t *testing.T, path, speaker string, embedding []float64, secondsUsed float64, segmentsUsed int) {
	t.Helper()
	dimensions := 0
	if len(embedding) > 0 {
		dimensions = len(embedding)
	}
	payload := map[string]any{
		"dimensions": dimensions,
		"speakers": []map[string]any{
			{
				"speaker":       speaker,
				"embedding":     embedding,
				"seconds_used":  secondsUsed,
				"segments_used": segmentsUsed,
			},
		},
	}
	data, err := json.Marshal(payload)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, data, 0644))
}

func writeSpeakerEmbeddingOutputFixtureRaw(t *testing.T, path, raw string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, []byte(strings.TrimSpace(raw)), 0644))
}
