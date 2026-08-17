package adapters

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactedCommandMasksSecretFlagValues(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "whisperx hf_token",
			args: []string{"python", "-m", "whisperx", "audio.wav", "--model", "large-v3", "--hf_token", "hf_realsecretvalue"},
			want: "python -m whisperx audio.wav --model large-v3 --hf_token ***",
		},
		{
			name: "pyannote hyphenated hf-token",
			args: []string{"diarize.py", "--hf-token", "hf_realsecretvalue", "--min-speakers", "2"},
			want: "diarize.py --hf-token *** --min-speakers 2",
		},
		{
			name: "equals form",
			args: []string{"run", "--api_key=sk-realsecretvalue", "--verbose"},
			want: "run --api_key=*** --verbose",
		},
		{
			name: "no secrets is unchanged",
			args: []string{"python", "-m", "whisperx", "--model", "small", "--language", "es"},
			want: "python -m whisperx --model small --language es",
		},
		{
			name: "trailing secret flag with no value does not panic",
			args: []string{"python", "--hf_token"},
			want: "python --hf_token",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, RedactedCommand(test.args))
		})
	}
}

func TestRedactedCommandDoesNotMutateCallerArgs(t *testing.T) {
	args := []string{"python", "--hf_token", "hf_realsecretvalue"}

	RedactedCommand(args)

	// The adapter passes this same slice to exec.Command after logging it.
	require.Equal(t, "hf_realsecretvalue", args[2])
}
