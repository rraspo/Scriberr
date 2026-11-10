package adapters

import (
	"archive/tar"
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scriberr/internal/models"
	"scriberr/internal/transcription/interfaces"

	"github.com/stretchr/testify/require"
)

func unreachableAddress(t *testing.T) string {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	return address
}

func whisperXOutputTar(t *testing.T, text string) []byte {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	data := []byte(`{"language":"es","segments":[{"start":0,"end":1,"text":"` + text + `"}],"word_segments":[]}`)
	require.NoError(t, writer.WriteHeader(&tar.Header{Name: "output/input.json", Mode: 0644, Size: int64(len(data))}))
	_, err := writer.Write(data)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return archive.Bytes()
}

type secondHostCase struct {
	adapter        *WhisperXAdapter
	secondHost     *fakeRemoteTransport
	localAttempted bool
	path, reason   string
}

func newSecondHostCase(t *testing.T, root string, secondHost *fakeRemoteTransport, secondHostReachable bool) *secondHostCase {
	testCase := &secondHostCase{adapter: NewWhisperXAdapter(root), secondHost: secondHost}
	primaryAddress := unreachableAddress(t)
	secondAddress := unreachableAddress(t)
	testCase.adapter.remoteTransportFactory = func(execution models.ProfileExecution) (RemoteTransport, error) {
		if execution.RemoteHost == "second-gpu.example" && secondHostReachable {
			return secondHost, nil
		}
		if execution.RemoteHost == "second-gpu.example" {
			return &dialFailureTransport{address: secondAddress}, nil
		}
		return &dialFailureTransport{address: primaryAddress}, nil
	}
	testCase.adapter.localFallback = func(_ context.Context, _ interfaces.AudioInput, params map[string]interface{}, _ interfaces.ProcessingContext) (*interfaces.TranscriptResult, error) {
		testCase.localAttempted = true
		require.Equal(t, "cpu", params["device"])
		return &interfaces.TranscriptResult{Text: "local transcript"}, nil
	}
	return testCase
}

func (c *secondHostCase) transcribe(t *testing.T, root string) (*interfaces.TranscriptResult, error) {
	audioPath := filepath.Join(root, "audio.wav")
	require.NoError(t, os.WriteFile(audioPath, []byte("audio"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "job"), 0755))
	return c.adapter.Transcribe(context.Background(), interfaces.AudioInput{FilePath: audioPath, Format: "wav", Size: 5},
		map[string]interface{}{"model": "large-v3", "language": "es", "diarize": true},
		interfaces.ProcessingContext{
			JobID: "second-host-job", TempDirectory: filepath.Join(root, "temp"), OutputDirectory: filepath.Join(root, "job"),
			Execution:          models.ProfileExecution{Mode: "remote", RemoteHost: "primary-gpu.example", RemoteWorkDir: "/srv/primary"},
			FallbackParameters: map[string]interface{}{"model": "large-v3-turbo", "language": "es", "diarize": true},
			FallbackExecution:  models.ProfileExecution{Mode: "remote", RemoteHost: "second-gpu.example", RemoteWorkDir: "/srv/second"},
			RecordExecutionPath: func(executedPath, executedReason string) {
				c.path, c.reason = executedPath, executedReason
			},
		})
}

func TestUnreachablePrimaryGPUHostRunsOnTheFallbackProfilesGPUHost(t *testing.T) {
	root := t.TempDir()
	testCase := newSecondHostCase(t, root, &fakeRemoteTransport{tarData: whisperXOutputTar(t, "hola")}, true)

	result, err := testCase.transcribe(t, root)

	require.NoError(t, err)
	require.Equal(t, "hola", result.Text)
	require.False(t, testCase.localAttempted)
	require.Equal(t, "remote-fallback", testCase.path)
	require.Contains(t, testCase.reason, "second-gpu.example")
	require.NotEmpty(t, testCase.secondHost.calls)
	submit := testCase.secondHost.calls[0].command
	require.True(t, strings.HasPrefix(submit, "/srv/second/scriberr-remote-job.sh "), submit)
	require.Contains(t, submit, "--model large-v3-turbo")
}

func TestBothGPUHostsUnreachableFallsBackToLocalCPU(t *testing.T) {
	root := t.TempDir()
	testCase := newSecondHostCase(t, root, &fakeRemoteTransport{}, false)

	result, err := testCase.transcribe(t, root)

	require.NoError(t, err)
	require.Equal(t, "local transcript", result.Text)
	require.True(t, testCase.localAttempted)
	require.Equal(t, "local-fallback", testCase.path)
}

func TestFallbackGPUHostWrapperFailureFailsTheJob(t *testing.T) {
	root := t.TempDir()
	testCase := newSecondHostCase(t, root, &fakeRemoteTransport{err: exitStatusError(4)}, true)

	_, err := testCase.transcribe(t, root)

	require.Error(t, err)
	require.False(t, testCase.localAttempted)
	require.Equal(t, "remote-fallback", testCase.path)
}
