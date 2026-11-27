package api

import (
	"testing"

	"scriberr/internal/models"

	"github.com/stretchr/testify/require"
)

func describeTestJob(source string, title string, audioPath string) models.TranscriptionJob {
	job := models.TranscriptionJob{Source: source, AudioPath: audioPath}
	if title != "" {
		job.Title = &title
	}
	return job
}

func TestDescribeSourceUsesOnlyDataTheJobCarries(t *testing.T) {
	channel := "Example Channel"
	youtubeWithChannel := describeTestJob("youtube", "A guide video", "/data/uploads/a.mp3")
	youtubeWithChannel.SourceChannel = &channel
	multitrack := describeTestJob("manual", "Band practice", "/data/uploads/c")
	multitrack.IsMultiTrack = true

	cases := []struct {
		name  string
		job   models.TranscriptionJob
		kind  string
		label string
	}{
		{"youtube with channel", youtubeWithChannel, "youtube", "Example Channel · YouTube"},
		{"youtube without channel", describeTestJob("youtube", "Some video", "/data/uploads/b.mp3"), "youtube", "YouTube"},
		{"plaud stays plaud", describeTestJob("plaud", "2026-01-13 19:48:59 [plaud-000000000001]", "/data/uploads/p.ogg"), "plaud", "Plaud"},
		{"whatsapp inbox voice note", describeTestJob("whatsapp", "WhatsApp Audio 2026-01-25 at 11.28.53.ogg", "/data/uploads/w.ogg"), "whatsapp", "WhatsApp · Voice note"},
		{"whatsapp inbox audio file", describeTestJob("whatsapp", "WhatsApp Audio 2026-01-25 at 19.10.01", "/data/uploads/w.mp3"), "whatsapp", "WhatsApp · Audio"},
		{"whatsapp ptt uploaded by hand", describeTestJob("manual", "WhatsApp Ptt 2026-01-02 at 09.14.11", "/data/uploads/w.ogg"), "whatsapp", "WhatsApp · Voice note"},
		{"phone call with contact", describeTestJob("manual", "Call Example Bank Óffice_260119_135909", "/data/uploads/c.m4a"), "call", "Call · Example Bank Óffice"},
		{"phone call with a number", describeTestJob("manual", "Call +10000000000_260101_101010_01", "/data/uploads/c.m4a"), "call", "Call · +10000000000"},
		{"phone call named in spanish", describeTestJob("manual", "Llamada soporte", "/data/uploads/c.m4a"), "call", "Call"},
		{"phone voice memo", describeTestJob("manual", "Voice 260102_084512", "/data/uploads/v.m4a"), "voice_memo", "Voice memo"},
		{"phone voice memo with a name", describeTestJob("manual", "Alex 260130_181200", "/data/uploads/v.m4a"), "voice_memo", "Voice memo"},
		{"omi by source", describeTestJob("omi", "Conversation", "/data/uploads/o.wav"), "omi", "Omi"},
		{"omi by title", describeTestJob("manual", "Omi 2026-01-03 10:12", "/data/uploads/o.wav"), "omi", "Omi"},
		{"multitrack", multitrack, "multitrack", "Multitrack"},
		{"meeting upload", describeTestJob("manual", "Team sync 2026-01-17 14-12-02", "/data/uploads/m.mp3"), "upload", "Upload"},
		{"untitled upload", describeTestJob("manual", "", "/data/uploads/u.wav"), "upload", "Upload"},
		{"unknown source", describeTestJob("zoom", "Weekly", "/data/uploads/z.mp3"), "upload", "Upload"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			description := describeSource(testCase.job)
			require.Equal(t, testCase.kind, description.Kind)
			require.Equal(t, testCase.label, description.Label)
		})
	}
}

func TestDescribeSourceDoesNotMistakeAMemoNamingPatternInAnotherFormat(t *testing.T) {
	description := describeSource(describeTestJob("manual", "Voice 260102_084512", "/data/uploads/v.mp3"))
	require.Equal(t, "upload", description.Kind, "phone voice-recorder files are .m4a; the pattern alone is not enough")
}
