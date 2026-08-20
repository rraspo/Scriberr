package models

import "time"

// SpeakerProfile represents a named, reusable speaker identity that voice
// samples can be enrolled against for future diarization matching. Whether a
// profile is usable is derived from whether it has any samples, not stored
// as a flag on the profile itself.
type SpeakerProfile struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	Name      string    `json:"name" gorm:"type:varchar(255);uniqueIndex;not null"`
	Notes     string    `json:"notes" gorm:"type:text"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

// TableName pins the speaker profile table name explicitly.
func (SpeakerProfile) TableName() string {
	return "speaker_profiles"
}

// SpeakerProfileSample represents one voice embedding enrolled against a
// speaker profile, either from an explicit enrollment or from a user
// correcting a diarization label. The embedding is biometric-shaped data and
// must never be serialized to an API response or log line; json:"-" enforces
// that even if a sample is ever marshaled directly instead of through a
// handler-built response struct.
//
// SourceJobID intentionally has no foreign key relationship to
// TranscriptionJob: samples must survive deletion of the job they were
// captured from, so the reference is allowed to dangle.
type SpeakerProfileSample struct {
	ID                 uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	SpeakerProfileID   uint      `json:"speaker_profile_id" gorm:"not null;index"`
	Embedding          []byte    `json:"-" gorm:"type:blob;not null"`
	Dimensions         int       `json:"dimensions" gorm:"not null"`
	Source             string    `json:"source" gorm:"type:varchar(20);not null"` // "enrollment" or "correction"
	SourceJobID        string    `json:"source_job_id" gorm:"type:varchar(36)"`
	SourceSpeakerLabel string    `json:"source_speaker_label" gorm:"type:varchar(50)"`
	SecondsUsed        float64   `json:"seconds_used" gorm:"type:real"`
	CreatedAt          time.Time `json:"created_at" gorm:"autoCreateTime"`

	// Relationship: cascades on the profile side only. SourceJobID above is
	// deliberately not a relationship.
	SpeakerProfile SpeakerProfile `json:"-" gorm:"foreignKey:SpeakerProfileID;references:ID;constraint:OnDelete:CASCADE"`
}

// TableName pins the speaker profile sample table name explicitly.
func (SpeakerProfileSample) TableName() string {
	return "speaker_profile_samples"
}
