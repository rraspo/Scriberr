package api

import (
	"path/filepath"
	"regexp"
	"strings"

	"scriberr/internal/models"
)

// SourceDescription says where a job's audio came from, derived only from
// data the job already carries.
type SourceDescription struct {
	Kind  string
	Label string
}

var (
	phoneCallTitlePattern = regexp.MustCompile(`^Call (.+?)_\d{6}_\d{6}(_\d+)?$`)
	phoneMemoTitlePattern = regexp.MustCompile(`^.+ \d{6}_\d{6}$`)
)

func describeSource(job models.TranscriptionJob) SourceDescription {
	title := ""
	if job.Title != nil {
		title = *job.Title
	}
	extension := strings.ToLower(filepath.Ext(job.AudioPath))

	switch {
	case job.Source == sourceYouTube:
		if job.SourceChannel != nil && *job.SourceChannel != "" {
			return SourceDescription{"youtube", *job.SourceChannel + " · YouTube"}
		}
		return SourceDescription{"youtube", "YouTube"}
	case job.Source == sourceInstagram:
		return describeSocial("instagram", "Instagram", job)
	case job.Source == sourceTikTok:
		return describeSocial("tiktok", "TikTok", job)
	case job.Source == "plaud":
		return SourceDescription{"plaud", "Plaud"}
	case job.Source == "whatsapp" || strings.HasPrefix(title, "WhatsApp Ptt") || strings.HasPrefix(title, "WhatsApp Audio"):
		return SourceDescription{"whatsapp", "WhatsApp"}
	case job.Source == sourceVideo:
		return SourceDescription{"video", "Video"}
	case job.Source == "omi" || strings.HasPrefix(title, "Omi "):
		return SourceDescription{"omi", "Omi"}
	case job.IsMultiTrack:
		return SourceDescription{"multitrack", "Multitrack"}
	}
	if match := phoneCallTitlePattern.FindStringSubmatch(title); match != nil {
		return SourceDescription{"call", "Call · " + match[1]}
	}
	if strings.HasPrefix(strings.ToLower(title), "llamada") || strings.HasPrefix(title, "Call ") || title == "Call" {
		return SourceDescription{"call", "Call"}
	}
	if extension == ".m4a" && phoneMemoTitlePattern.MatchString(title) {
		return SourceDescription{"voice_memo", "Voice memo"}
	}
	return SourceDescription{"upload", "Upload"}
}

// applySourceDescription fills the job's non-persisted source fields.
func applySourceDescription(job *models.TranscriptionJob) {
	description := describeSource(*job)
	job.SourceKind = description.Kind
	job.SourceLabel = description.Label
}

// describeSocial labels an Instagram or TikTok job with its author handle.
func describeSocial(kind, name string, job models.TranscriptionJob) SourceDescription {
	if job.SourceChannel != nil && *job.SourceChannel != "" {
		return SourceDescription{kind, *job.SourceChannel + " · " + name}
	}
	return SourceDescription{kind, name}
}
