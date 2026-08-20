package adapters

import (
	"context"
	"embed"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"scriberr/internal/transcription/interfaces"
	"scriberr/pkg/logger"
)

//go:embed py/speaker/*
var speakerScripts embed.FS

// Segment selection mirrors the defaults baked into speaker_embed.py; passing
// them explicitly keeps the contract visible on the Go side and testable
// without relying on the script's own defaults.
const (
	speakerEmbeddingMaxSegments = 3
	speakerEmbeddingMaxSeconds  = 30.0
)

// SpeakerEmbeddingAdapter runs the embedded speaker_embed.py script to turn
// a single speaker's diarized segments into a voice embedding. It satisfies
// interfaces.SpeakerEmbeddingExtractor.
type SpeakerEmbeddingAdapter struct {
	envPath string

	// runCommand executes the uv invocation and returns its combined output.
	// Swappable in tests so extraction logic can be verified without a real
	// Python environment.
	runCommand func(ctx context.Context, args []string, env []string) ([]byte, error)

	// prepareEnvironment bootstraps the dedicated uv environment. Swappable
	// in tests so extraction logic can be verified without shelling out to
	// a real uv/Python toolchain.
	prepareEnvironment func(ctx context.Context) error
}

// NewSpeakerEmbeddingAdapter creates a new speaker embedding adapter rooted
// at envPath, the dedicated uv environment directory for this extractor.
func NewSpeakerEmbeddingAdapter(envPath string) *SpeakerEmbeddingAdapter {
	adapter := &SpeakerEmbeddingAdapter{envPath: envPath}
	adapter.runCommand = adapter.defaultRunCommand
	adapter.prepareEnvironment = adapter.defaultPrepareEnvironment
	return adapter
}

func (a *SpeakerEmbeddingAdapter) defaultRunCommand(ctx context.Context, args []string, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "uv", args...)
	cmd.Env = env
	return cmd.CombinedOutput()
}

// speakerSegmentJSON mirrors the shape speaker_embed.py expects for
// --segments: a plain JSON array of {"speaker","start","end"}.
type speakerSegmentJSON struct {
	Speaker string  `json:"speaker"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
}

// speakerEmbeddingOutput mirrors speaker_embed.py's --output JSON.
type speakerEmbeddingOutput struct {
	Dimensions int                           `json:"dimensions"`
	Speakers   []speakerEmbeddingOutputEntry `json:"speakers"`
}

type speakerEmbeddingOutputEntry struct {
	Speaker      string    `json:"speaker"`
	Embedding    []float64 `json:"embedding"`
	SecondsUsed  float64   `json:"seconds_used"`
	SegmentsUsed int       `json:"segments_used"`
}

// ExtractSpeakerEmbedding prepares the dedicated Python environment if
// needed, then runs speaker_embed.py over audioPath restricted to the given
// segments (expected to already be scoped to a single speaker label).
func (a *SpeakerEmbeddingAdapter) ExtractSpeakerEmbedding(ctx context.Context, audioPath string, segments []interfaces.SpeakerSegment) (*interfaces.SpeakerEmbeddingResult, error) {
	if len(segments) == 0 {
		return nil, fmt.Errorf("no segments provided for speaker embedding extraction")
	}
	speakerLabel := segments[0].Speaker

	if err := a.prepareEnvironment(ctx); err != nil {
		return nil, fmt.Errorf("failed to prepare speaker embedding environment: %w", err)
	}

	tempDir, err := os.MkdirTemp("", "speaker-embed-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	segmentsPath := filepath.Join(tempDir, "segments.json")
	outputPath := filepath.Join(tempDir, "output.json")
	if err := writeSpeakerSegmentsFile(segmentsPath, segments); err != nil {
		return nil, fmt.Errorf("failed to write segments file: %w", err)
	}

	args := a.buildArgs(audioPath, segmentsPath, outputPath)

	// TORCH_FORCE_NO_WEIGHTS_ONLY_LOAD works around torch>=2.6 defaulting
	// weights_only=True, which breaks unpickling the pyannote checkpoint.
	env := append(os.Environ(), "PYTHONUNBUFFERED=1", "TORCH_FORCE_NO_WEIGHTS_ONLY_LOAD=1")

	logger.Info("Executing speaker embedding command", "args", RedactedCommand(args))

	output, err := a.runCommand(ctx, args, env)
	if err != nil {
		return nil, fmt.Errorf("speaker embedding extraction failed: %w: %s", err, strings.TrimSpace(string(output)))
	}

	return parseSpeakerEmbeddingOutput(outputPath, speakerLabel)
}

// buildArgs builds the uv invocation for speaker_embed.py.
func (a *SpeakerEmbeddingAdapter) buildArgs(audioPath, segmentsPath, outputPath string) []string {
	scriptPath := filepath.Join(a.envPath, "speaker_embed.py")
	return []string{
		"run", "--native-tls", "--project", a.envPath, "python", scriptPath,
		"--audio", audioPath,
		"--segments", segmentsPath,
		"--output", outputPath,
		"--max-segments", strconv.Itoa(speakerEmbeddingMaxSegments),
		"--max-seconds", fmt.Sprintf("%.1f", speakerEmbeddingMaxSeconds),
		"--device", "cpu",
	}
}

// defaultPrepareEnvironment writes the embedded script and pyproject.toml,
// then syncs the dedicated uv environment if it is not already ready.
// Readiness is cached by CheckEnvironmentReady so this is cheap on the
// common path.
func (a *SpeakerEmbeddingAdapter) defaultPrepareEnvironment(ctx context.Context) error {
	if err := a.writeEmbeddedFiles(); err != nil {
		return err
	}

	if CheckEnvironmentReady(a.envPath, "from pyannote.audio import Model") {
		return nil
	}

	if err := a.syncEnvironment(ctx); err != nil {
		return err
	}

	return nil
}

func (a *SpeakerEmbeddingAdapter) writeEmbeddedFiles() error {
	if err := os.MkdirAll(a.envPath, 0755); err != nil {
		return fmt.Errorf("failed to create speaker embedding directory: %w", err)
	}

	pyprojectContent, err := speakerScripts.ReadFile("py/speaker/pyproject.toml")
	if err != nil {
		return fmt.Errorf("failed to read embedded pyproject.toml: %w", err)
	}
	// Replace the hardcoded PyTorch URL with the dynamic one based on
	// environment, matching the PyAnnote adapter's setup.
	contentStr := strings.Replace(
		string(pyprojectContent),
		"https://download.pytorch.org/whl/cu126",
		GetPyTorchWheelURL(),
		1,
	)
	pyprojectPath := filepath.Join(a.envPath, "pyproject.toml")
	if err := os.WriteFile(pyprojectPath, []byte(contentStr), 0644); err != nil {
		return fmt.Errorf("failed to write pyproject.toml: %w", err)
	}

	scriptContent, err := speakerScripts.ReadFile("py/speaker/speaker_embed.py")
	if err != nil {
		return fmt.Errorf("failed to read embedded speaker_embed.py: %w", err)
	}
	scriptPath := filepath.Join(a.envPath, "speaker_embed.py")
	if err := os.WriteFile(scriptPath, scriptContent, 0755); err != nil {
		return fmt.Errorf("failed to write speaker_embed.py: %w", err)
	}

	return nil
}

func (a *SpeakerEmbeddingAdapter) syncEnvironment(ctx context.Context) error {
	logger.Info("Installing speaker embedding dependencies")
	cmd := exec.CommandContext(ctx, "uv", "sync", "--native-tls")
	cmd.Dir = a.envPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("uv sync failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func writeSpeakerSegmentsFile(path string, segments []interfaces.SpeakerSegment) error {
	payload := make([]speakerSegmentJSON, len(segments))
	for i, segment := range segments {
		payload[i] = speakerSegmentJSON{Speaker: segment.Speaker, Start: segment.Start, End: segment.End}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to encode segments: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// parseSpeakerEmbeddingOutput reads speaker_embed.py's output JSON and
// converts the requested speaker's entry into an interfaces.SpeakerEmbeddingResult.
// A null embedding in the output (the speaker fell under the audio floor)
// becomes a nil Embedding, not an error.
func parseSpeakerEmbeddingOutput(outputPath, speakerLabel string) (*interfaces.SpeakerEmbeddingResult, error) {
	data, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read speaker embedding output: %w", err)
	}

	var parsed speakerEmbeddingOutput
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse speaker embedding output: %w", err)
	}

	for _, entry := range parsed.Speakers {
		if entry.Speaker != speakerLabel {
			continue
		}
		result := &interfaces.SpeakerEmbeddingResult{
			Dimensions:   parsed.Dimensions,
			SecondsUsed:  entry.SecondsUsed,
			SegmentsUsed: entry.SegmentsUsed,
		}
		if len(entry.Embedding) > 0 {
			result.Embedding = encodeEmbeddingLittleEndianFloat32(entry.Embedding)
		}
		return result, nil
	}

	return nil, fmt.Errorf("speaker embedding output missing entry for speaker %q", speakerLabel)
}

// encodeEmbeddingLittleEndianFloat32 encodes an embedding vector as
// little-endian float32 bytes, the BLOB format SpeakerProfileSample stores.
func encodeEmbeddingLittleEndianFloat32(values []float64) []byte {
	buffer := make([]byte, 4*len(values))
	for i, value := range values {
		binary.LittleEndian.PutUint32(buffer[i*4:], math.Float32bits(float32(value)))
	}
	return buffer
}
