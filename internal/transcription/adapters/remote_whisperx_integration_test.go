package adapters

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"scriberr/internal/models"

	"github.com/stretchr/testify/require"
)

func TestRemoteWhisperXIntegration(t *testing.T) {
	host := os.Getenv("SCRIBERR_REMOTE_TEST_HOST")
	user := os.Getenv("SCRIBERR_REMOTE_TEST_USER")
	key := os.Getenv("SCRIBERR_REMOTE_TEST_KEY")
	workDir := os.Getenv("SCRIBERR_REMOTE_TEST_WORKDIR")
	if host == "" || user == "" || key == "" || workDir == "" {
		t.Skip("set SCRIBERR_REMOTE_TEST_HOST/_USER/_KEY/_WORKDIR to run")
	}
	model := os.Getenv("SCRIBERR_REMOTE_TEST_MODEL")
	if model == "" {
		model = "tiny"
	}
	root := t.TempDir()
	audioPath := filepath.Join(root, "tiny.wav")
	require.NoError(t, writeTinyWAV(audioPath))
	profile := models.ProfileExecution{Mode: "remote", RemoteHost: host, RemotePort: 22, RemoteUser: user, RemoteKeyPath: key,
		RemoteWorkDir: workDir, RemoteCommandPrefix: os.Getenv("SCRIBERR_REMOTE_TEST_PREFIX"), ConnectTimeoutSeconds: 10}
	transport, err := NewSSHTransport(profile)
	require.NoError(t, err)
	executor := NewRemoteWhisperXExecutor(transport, profile)
	outputDir := filepath.Join(root, "output")
	require.NoError(t, executor.Execute(context.Background(), "scriberr-integration-test", audioPath,
		map[string]interface{}{"model": model, "language": "en", "diarize": true, "min_speakers": 1, "max_speakers": 2},
		outputDir, filepath.Join(root, "transcription.log")))
	matches, err := filepath.Glob(filepath.Join(outputDir, "*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, matches)
}

func writeTinyWAV(path string) error {
	data := make([]byte, 16000*2)
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(36+len(data)))
	copy(header[8:16], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], 1)
	binary.LittleEndian.PutUint32(header[24:28], 16000)
	binary.LittleEndian.PutUint32(header[28:32], 32000)
	binary.LittleEndian.PutUint16(header[32:34], 2)
	binary.LittleEndian.PutUint16(header[34:36], 16)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], uint32(len(data)))
	if _, err := file.Write(header); err != nil {
		return err
	}
	_, err = file.Write(data)
	return err
}
