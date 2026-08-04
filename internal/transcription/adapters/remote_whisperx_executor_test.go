package adapters

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scriberr/internal/models"
	"scriberr/internal/transcription/interfaces"

	"github.com/stretchr/testify/require"
)

type transportCall struct {
	command string
	stdin   string
}

type fakeRemoteTransport struct {
	calls      []transportCall
	tarData    []byte
	failOnCall int
}

func (f *fakeRemoteTransport) Exec(_ context.Context, command string, stdin io.Reader, stdout, _ io.Writer) error {
	call := transportCall{command: command}
	if stdin != nil {
		data, _ := io.ReadAll(stdin)
		call.stdin = string(data)
	}
	f.calls = append(f.calls, call)
	if f.failOnCall == len(f.calls) {
		return errors.New("remote test failure")
	}
	if stdout != nil && strings.Contains(command, "tar -C") {
		_, _ = stdout.Write(f.tarData)
	}
	return nil
}

func outputArchive(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	tw := tar.NewWriter(&buffer)
	data := []byte(`{"language":"es","segments":[]}`)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "output/audio.json", Mode: 0644, Size: int64(len(data))}))
	_, err := tw.Write(data)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	return buffer.Bytes()
}

func TestRemoteWhisperXExecutorSubmitRetrieveCleanup(t *testing.T) {
	root := t.TempDir()
	audioPath := filepath.Join(root, "audio.wav")
	require.NoError(t, os.WriteFile(audioPath, []byte("audio-stream"), 0644))
	transport := &fakeRemoteTransport{tarData: outputArchive(t)}
	executor := NewRemoteWhisperXExecutor(transport, models.ProfileExecution{RemoteWorkDir: "/srv/scriberr-work"})
	params := map[string]interface{}{"model": "large-v3", "language": "es", "diarize": true, "min_speakers": 2, "max_speakers": 6}

	err := executor.Execute(context.Background(), "job-123", audioPath, params, filepath.Join(root, "work"), filepath.Join(root, "transcription.log"))

	require.NoError(t, err)
	require.Len(t, transport.calls, 3)
	require.Equal(t, "/srv/scriberr-work/scriberr-remote-job.sh --job-id job-123 --model large-v3 --language es --diarize --min-speakers 2 --max-speakers 6", transport.calls[0].command)
	require.Equal(t, "audio-stream", transport.calls[0].stdin)
	require.Equal(t, "tar -C /srv/scriberr-work/jobs/job-123 -cf - output", transport.calls[1].command)
	require.Equal(t, "rm -r -- /srv/scriberr-work/jobs/job-123", transport.calls[2].command)
	_, err = os.Stat(filepath.Join(root, "work", "audio.json"))
	require.NoError(t, err)
}

func TestRemoteWhisperXExecutorCleansUpWhenRetrieveFails(t *testing.T) {
	root := t.TempDir()
	audioPath := filepath.Join(root, "audio.wav")
	require.NoError(t, os.WriteFile(audioPath, []byte("audio"), 0644))
	transport := &fakeRemoteTransport{failOnCall: 2}
	executor := NewRemoteWhisperXExecutor(transport, models.ProfileExecution{RemoteWorkDir: "/srv/scriberr-work"})

	err := executor.Execute(context.Background(), "job-456", audioPath, map[string]interface{}{"model": "small", "language": "en"}, root, filepath.Join(root, "transcription.log"))

	var remoteErr *RemoteFailure
	require.ErrorAs(t, err, &remoteErr)
	require.Equal(t, "retrieve", remoteErr.Stage)
	require.Len(t, transport.calls, 3)
	require.Equal(t, "rm -r -- /srv/scriberr-work/jobs/job-456", transport.calls[2].command)
}

func TestRemoteWhisperXSubmitCommandForwardsAllProfileParameters(t *testing.T) {
	tests := []struct {
		name, prefix, want string
	}{
		{"without prefix", "", "/srv/scriberr-work/scriberr-remote-job.sh --job-id job-1 --model large-v3 --language es --diarize --min-speakers 2 --max-speakers 6"},
		{"with prefix", "wsl -d Ubuntu --", "wsl -d Ubuntu -- /srv/scriberr-work/scriberr-remote-job.sh --job-id job-1 --model large-v3 --language es --diarize --min-speakers 2 --max-speakers 6"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := NewRemoteWhisperXExecutor(nil, models.ProfileExecution{RemoteWorkDir: "/srv/scriberr-work", RemoteCommandPrefix: test.prefix})
			params := map[string]interface{}{"model": "large-v3", "language": "es", "diarize": true, "min_speakers": 2, "max_speakers": 6}
			require.Equal(t, test.want, executor.submitCommand("job-1", params))
		})
	}
}

func TestWhisperXAdapterRemotePathUsesExistingResultParser(t *testing.T) {
	root := t.TempDir()
	audioPath := filepath.Join(root, "audio.wav")
	require.NoError(t, os.WriteFile(audioPath, []byte("audio-stream"), 0644))
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	data := []byte(`{"language":"es","segments":[{"start":0,"end":1,"text":"hola","speaker":"SPEAKER_00"}],"word_segments":[{"start":0,"end":1,"word":"hola","score":0.9,"speaker":"SPEAKER_00"}]}`)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "output/audio.json", Mode: 0644, Size: int64(len(data))}))
	_, err := tw.Write(data)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	transport := &fakeRemoteTransport{tarData: archive.Bytes()}
	adapter := NewWhisperXAdapter(root)
	adapter.remoteTransportFactory = func(models.ProfileExecution) (RemoteTransport, error) { return transport, nil }
	require.NoError(t, os.MkdirAll(filepath.Join(root, "job"), 0755))

	result, err := adapter.Transcribe(context.Background(), interfaces.AudioInput{FilePath: audioPath, Format: "wav", Size: int64(len("audio-stream"))},
		map[string]interface{}{"model": "small", "language": "es", "diarize": true}, interfaces.ProcessingContext{
			JobID: "job-parser", TempDirectory: filepath.Join(root, "temp"), OutputDirectory: filepath.Join(root, "job"),
			Execution: models.ProfileExecution{Mode: "remote", RemoteWorkDir: "/srv/scriberr-work"},
		})

	require.NoError(t, err)
	require.Equal(t, "hola", result.Text)
	require.Len(t, result.Segments, 1)
	require.Equal(t, "SPEAKER_00", *result.Segments[0].Speaker)
	require.Equal(t, "SPEAKER_00", *result.WordSegments[0].Speaker)
}
