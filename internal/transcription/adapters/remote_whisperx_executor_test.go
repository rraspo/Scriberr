package adapters

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	err        error
}

type exitStatusError int

func (e exitStatusError) Error() string   { return "remote command failed" }
func (e exitStatusError) ExitStatus() int { return int(e) }

func TestClassifyRemoteFailure(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		fallback bool
	}{
		{name: "connection refused", err: &RemoteFailure{Stage: "submit", Err: &net.OpError{Op: "dial", Err: errors.New("connection refused")}}, fallback: true},
		{name: "DNS failure", err: &RemoteFailure{Stage: "submit", Err: &net.DNSError{Err: "no such host", Name: "gpu-host.example"}}, fallback: true},
		{name: "auth failure", err: &RemoteFailure{Stage: "submit", Err: errors.New("ssh: handshake failed: unable to authenticate")}, fallback: true},
		{name: "connect timeout", err: &RemoteFailure{Stage: "submit", Err: context.DeadlineExceeded}, fallback: true},
		{name: "wrapper not found", err: &RemoteFailure{Stage: "submit", Err: exitStatusError(127)}, fallback: true},
		{name: "wrapper input error", err: &RemoteFailure{Stage: "submit", Err: exitStatusError(2)}},
		{name: "wrapper transcription error", err: &RemoteFailure{Stage: "submit", Err: exitStatusError(3)}},
		{name: "wrapper packaging error", err: &RemoteFailure{Stage: "submit", Err: exitStatusError(4)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := classifyRemoteFailure(test.err)
			require.Equal(t, test.fallback, decision.Fallback)
			require.NotEmpty(t, decision.Reason)
		})
	}
}

func TestRemoteUnavailableFallsBackToLocal(t *testing.T) {
	root := t.TempDir()
	audioPath := filepath.Join(root, "audio.wav")
	require.NoError(t, os.WriteFile(audioPath, []byte("audio"), 0644))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	adapter := NewWhisperXAdapter(root)
	adapter.remoteTransportFactory = func(models.ProfileExecution) (RemoteTransport, error) {
		return &dialFailureTransport{address: net.JoinHostPort("127.0.0.1", fmt.Sprint(port))}, nil
	}
	adapter.localFallback = func(_ context.Context, _ interfaces.AudioInput, params map[string]interface{}, _ interfaces.ProcessingContext) (*interfaces.TranscriptResult, error) {
		require.Equal(t, "cpu", params["device"])
		require.Equal(t, "float32", params["compute_type"])
		return &interfaces.TranscriptResult{Text: "local transcript"}, nil
	}
	var path, reason string
	result, err := adapter.Transcribe(context.Background(), interfaces.AudioInput{FilePath: audioPath, Format: "wav", Size: 5},
		map[string]interface{}{"model": "small", "device": "cuda", "compute_type": "float16"}, interfaces.ProcessingContext{JobID: "fallback-job", OutputDirectory: root,
			Execution:           models.ProfileExecution{Mode: "remote", RemoteHost: "127.0.0.1", RemotePort: port},
			RecordExecutionPath: func(executedPath, executedReason string) { path, reason = executedPath, executedReason }})
	require.NoError(t, err)
	require.Equal(t, "local transcript", result.Text)
	require.Equal(t, "local-fallback", path)
	require.NotEmpty(t, reason)
}

func TestRemoteWrapperFailureDoesNotFallBack(t *testing.T) {
	root := t.TempDir()
	audioPath := filepath.Join(root, "audio.wav")
	require.NoError(t, os.WriteFile(audioPath, []byte("audio"), 0644))
	transport := &fakeRemoteTransport{}
	transport.err = exitStatusError(4)
	adapter := NewWhisperXAdapter(root)
	adapter.remoteTransportFactory = func(models.ProfileExecution) (RemoteTransport, error) { return transport, nil }
	localAttempted := false
	adapter.localFallback = func(context.Context, interfaces.AudioInput, map[string]interface{}, interfaces.ProcessingContext) (*interfaces.TranscriptResult, error) {
		localAttempted = true
		return nil, nil
	}
	var path string
	_, err := adapter.Transcribe(context.Background(), interfaces.AudioInput{FilePath: audioPath, Format: "wav", Size: 5},
		map[string]interface{}{"model": "small"}, interfaces.ProcessingContext{JobID: "failed-job", OutputDirectory: root,
			Execution:           models.ProfileExecution{Mode: "remote", RemoteWorkDir: "/srv/scriberr-work"},
			RecordExecutionPath: func(executedPath, _ string) { path = executedPath }})
	require.Error(t, err)
	require.False(t, localAttempted)
	require.Equal(t, "remote", path)
}

func TestSSHTransportHonorsConnectTimeout(t *testing.T) {
	transport := &sshTransport{profile: models.ProfileExecution{RemoteHost: "10.255.255.1", RemotePort: 22, ConnectTimeoutSeconds: 1}}
	start := time.Now()
	_, err := transport.dial(context.Background(), "10.255.255.1:22", transport.connectTimeout())
	require.Error(t, err)
	require.Less(t, time.Since(start), 2*time.Second)
}

type dialFailureTransport struct{ address string }

func (t *dialFailureTransport) Exec(ctx context.Context, _ string, _ io.Reader, _, _ io.Writer) error {
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", t.address)
	if conn != nil {
		_ = conn.Close()
	}
	return err
}

func (f *fakeRemoteTransport) Exec(_ context.Context, command string, stdin io.Reader, stdout, _ io.Writer) error {
	call := transportCall{command: command}
	if stdin != nil {
		data, _ := io.ReadAll(stdin)
		call.stdin = string(data)
	}
	f.calls = append(f.calls, call)
	if f.err != nil {
		return f.err
	}
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

func TestRemoteWhisperXSubmitCommandOmitsUnsetLanguage(t *testing.T) {
	tests := []struct {
		name     string
		language interface{}
	}{
		{"absent", nil},
		{"empty", ""},
		{"blank", "   "},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := NewRemoteWhisperXExecutor(nil, models.ProfileExecution{RemoteWorkDir: "/srv/scriberr-work"})
			params := map[string]interface{}{"model": "small"}
			if test.language != nil {
				params["language"] = test.language
			}

			command := executor.submitCommand("job-1", params)

			// A flag with no value makes the remote wrapper exit 1 with no stderr.
			require.NotContains(t, command, "--language")
			require.Equal(t, "/srv/scriberr-work/scriberr-remote-job.sh --job-id job-1 --model small", command)
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
