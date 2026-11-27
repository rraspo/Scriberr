package adapters

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"scriberr/internal/models"

	"github.com/stretchr/testify/require"
)

// A wrapper that gave up waiting for room on a shared card exits 5; the job
// moves to the next tier instead of waiting forever or failing.
func TestClassifyBusyGPUHostFallsThrough(t *testing.T) {
	decision := classifyRemoteFailure(&RemoteFailure{Stage: "submit", Err: exitStatusError(5)})

	require.True(t, decision.Fallback)
	require.Equal(t, "remote GPU busy", decision.Reason)
}

func TestBusyFallbackGPUHostFallsBackToLocalCPU(t *testing.T) {
	root := t.TempDir()
	testCase := newSecondHostCase(t, root, &fakeRemoteTransport{err: exitStatusError(5)}, true)

	result, err := testCase.transcribe(t, root)

	require.NoError(t, err)
	require.Equal(t, "local transcript", result.Text)
	require.True(t, testCase.localAttempted)
	require.Equal(t, "local-fallback", testCase.path)
	require.Contains(t, testCase.reason, "remote GPU busy on second-gpu.example")
}

// blockingSubmitTransport holds the submit exec open until its context is
// cancelled, the way a wrapper waiting for GPU room does.
type blockingSubmitTransport struct {
	mutex     sync.Mutex
	commands  []string
	submitted chan struct{}
}

func (b *blockingSubmitTransport) Exec(ctx context.Context, command string, stdin io.Reader, _, _ io.Writer) error {
	b.mutex.Lock()
	b.commands = append(b.commands, command)
	first := len(b.commands) == 1
	b.mutex.Unlock()
	if !first {
		return nil
	}
	if stdin != nil {
		_, _ = io.ReadAll(stdin)
	}
	close(b.submitted)
	<-ctx.Done()
	return ctx.Err()
}

// Closing the SSH connection does not stop the wrapper on the host, so a
// cancelled run must stop it explicitly before removing its directory;
// otherwise it keeps the card's lock and every later job waits behind it.
func TestCancelledRemoteRunIsStoppedOnTheHost(t *testing.T) {
	root := t.TempDir()
	audioPath := filepath.Join(root, "audio.wav")
	require.NoError(t, os.WriteFile(audioPath, []byte("audio"), 0644))
	transport := &blockingSubmitTransport{submitted: make(chan struct{})}
	executor := NewRemoteWhisperXExecutor(transport, models.ProfileExecution{RemoteWorkDir: "/srv/scriberr-work"})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- executor.Execute(ctx, "cancel-job", audioPath, map[string]interface{}{"model": "small"}, root, filepath.Join(root, "transcription.log"))
	}()
	<-transport.submitted
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Execute did not return after cancellation")
	}

	require.Len(t, transport.commands, 3, transport.commands)
	runID := regexp.MustCompile(`--job-id (cancel-job-[0-9a-z]+)`).FindStringSubmatch(transport.commands[0])
	require.NotNil(t, runID, transport.commands[0])
	// The bracketed first letter keeps pkill from matching the shell that runs it.
	require.Equal(t, "pkill -f -- '[c]"+runID[1][1:]+"'", transport.commands[1])
	require.Equal(t, "rm -r -- /srv/scriberr-work/jobs/"+runID[1], transport.commands[2])
}

func TestCompletedRemoteRunIsNotStopped(t *testing.T) {
	root := t.TempDir()
	audioPath := filepath.Join(root, "audio.wav")
	require.NoError(t, os.WriteFile(audioPath, []byte("audio"), 0644))
	transport := &fakeRemoteTransport{tarData: outputArchive(t)}
	executor := NewRemoteWhisperXExecutor(transport, models.ProfileExecution{RemoteWorkDir: "/srv/scriberr-work"})

	require.NoError(t, executor.Execute(context.Background(), "done-job", audioPath, map[string]interface{}{"model": "small"}, filepath.Join(root, "work"), filepath.Join(root, "transcription.log")))

	for _, call := range transport.calls {
		require.NotContains(t, call.command, "pkill")
	}
}
