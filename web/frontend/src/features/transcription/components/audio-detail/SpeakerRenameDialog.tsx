import React, { useState, useEffect, useCallback } from 'react';
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Card, CardContent } from '@/components/ui/card';
import { Loader2, Users, Save, X, Mic2, Sparkles } from 'lucide-react';
import { useAuth } from "@/features/auth/hooks/useAuth";

interface SpeakerMapping {
  id?: number;
  original_speaker: string;
  custom_name: string;
  source?: string;
  confidence?: number;
  speaker_profile_id?: number;
}

interface VoiceProfile {
  id: number;
  name: string;
  sample_count: number;
}

interface SpeakerProvenance {
  source: string;
  confidence?: number;
}

interface SpeakerRenameDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  transcriptionId: string;
  onSpeakerMappingsUpdate: (mappings: SpeakerMapping[]) => void;
  initialSpeakers?: string[]; // Detected speakers from transcript
}

const NEW_PROFILE_OPTION = 'new';

const SpeakerRenameDialog: React.FC<SpeakerRenameDialogProps> = ({
  open,
  onOpenChange,
  transcriptionId,
  onSpeakerMappingsUpdate,
  initialSpeakers = [],
}) => {
  const { getAuthHeaders } = useAuth();
  const [speakerMappings, setSpeakerMappings] = useState<Record<string, string>>({});
  const [provenanceBySpeaker, setProvenanceBySpeaker] = useState<Record<string, SpeakerProvenance>>({});
  const [voiceProfiles, setVoiceProfiles] = useState<VoiceProfile[]>([]);
  const [enrollTarget, setEnrollTarget] = useState<string | null>(null);
  const [enrollProfileChoice, setEnrollProfileChoice] = useState<string>(NEW_PROFILE_OPTION);
  const [newProfileName, setNewProfileName] = useState('');
  const [isEnrolling, setIsEnrolling] = useState(false);
  const [enrollSuccess, setEnrollSuccess] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [isSaving, setIsSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const fetchSpeakerMappings = useCallback(async () => {
    setIsLoading(true);
    setError(null);

    try {
      const response = await fetch(`/api/v1/transcription/${transcriptionId}/speakers`, {
        headers: { ...getAuthHeaders() },
      });

      if (!response.ok) {
        throw new Error(`Failed to fetch speaker mappings: ${response.statusText}`);
      }

      const existingMappings: SpeakerMapping[] = await response.json();

      // Create a mapping object from the response
      const mappingObj: Record<string, string> = {};
      const provenanceObj: Record<string, SpeakerProvenance> = {};

      // Initialize with existing mappings
      existingMappings.forEach(mapping => {
        mappingObj[mapping.original_speaker] = mapping.custom_name;
        if (mapping.source) {
          provenanceObj[mapping.original_speaker] = {
            source: mapping.source,
            confidence: mapping.confidence,
          };
        }
      });

      // Add any speakers from the transcript that don't have mappings yet
      initialSpeakers.forEach(speaker => {
        if (!mappingObj[speaker]) {
          mappingObj[speaker] = speaker; // Default to original name
        }
      });

      setSpeakerMappings(mappingObj);
      setProvenanceBySpeaker(provenanceObj);
    } catch (err) {
      console.error('Error fetching speaker mappings:', err);
      setError(err instanceof Error ? err.message : 'Failed to fetch speaker mappings');

      // Initialize with default mappings if fetch fails
      const defaultMappings: Record<string, string> = {};
      initialSpeakers.forEach(speaker => {
        defaultMappings[speaker] = speaker;
      });
      setSpeakerMappings(defaultMappings);
    } finally {
      setIsLoading(false);
    }
  }, [transcriptionId, getAuthHeaders, initialSpeakers]);

  const fetchVoiceProfiles = useCallback(async () => {
    try {
      const response = await fetch('/api/v1/speakers/profiles', {
        headers: { ...getAuthHeaders() },
      });
      if (!response.ok) return;
      setVoiceProfiles(await response.json());
    } catch {
      // The enroll control degrades to "create new voice" when profiles
      // cannot be listed; enrollment itself will surface any real error.
    }
  }, [getAuthHeaders]);

  // Initialize speaker mappings when dialog opens
  useEffect(() => {
    if (open && transcriptionId) {
      fetchSpeakerMappings();
      fetchVoiceProfiles();
      setEnrollTarget(null);
      setEnrollSuccess(null);
    }
  }, [open, transcriptionId, fetchSpeakerMappings, fetchVoiceProfiles]);

  const handleSpeakerNameChange = (originalSpeaker: string, customName: string) => {
    setSpeakerMappings(prev => ({
      ...prev,
      [originalSpeaker]: customName,
    }));
  };

  const handleEnrollToggle = (speaker: string) => {
    setEnrollSuccess(null);
    setError(null);
    setEnrollTarget(current => (current === speaker ? null : speaker));
    setEnrollProfileChoice(voiceProfiles.length > 0 ? String(voiceProfiles[0].id) : NEW_PROFILE_OPTION);
    setNewProfileName('');
  };

  const handleEnroll = async (speaker: string) => {
    setIsEnrolling(true);
    setError(null);
    setEnrollSuccess(null);

    try {
      let profileId: number;
      let profileName: string;

      if (enrollProfileChoice === NEW_PROFILE_OPTION) {
        const trimmedName = newProfileName.trim();
        if (!trimmedName) {
          setError('Name the new voice before enrolling.');
          setIsEnrolling(false);
          return;
        }
        const createResponse = await fetch('/api/v1/speakers/profiles', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', ...getAuthHeaders() },
          body: JSON.stringify({ name: trimmedName }),
        });
        if (!createResponse.ok) {
          const body = await createResponse.json().catch(() => null);
          throw new Error(body?.error || 'Failed to create the voice profile.');
        }
        const created: VoiceProfile = await createResponse.json();
        profileId = created.id;
        profileName = trimmedName;
      } else {
        profileId = Number(enrollProfileChoice);
        profileName = voiceProfiles.find(profile => profile.id === profileId)?.name ?? 'this voice';
      }

      const enrollResponse = await fetch(`/api/v1/speakers/profiles/${profileId}/samples`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', ...getAuthHeaders() },
        body: JSON.stringify({ job_id: transcriptionId, speaker_label: speaker }),
      });
      if (!enrollResponse.ok) {
        const body = await enrollResponse.json().catch(() => null);
        throw new Error(body?.error || 'Failed to enroll this voice.');
      }

      setEnrollSuccess(`Enrolled ${speakerMappings[speaker] || speaker} as "${profileName}".`);
      setEnrollTarget(null);
      fetchVoiceProfiles();
    } catch (err) {
      console.error('Error enrolling voice:', err);
      setError(err instanceof Error ? err.message : 'Failed to enroll this voice.');
    } finally {
      setIsEnrolling(false);
    }
  };

  const saveSpeakerMappings = async () => {
    setIsSaving(true);
    setError(null);

    try {
      // Convert mappings to API format
      const mappingsArray = Object.entries(speakerMappings).map(([original_speaker, custom_name]) => ({
        original_speaker,
        custom_name,
      }));

      const response = await fetch(`/api/v1/transcription/${transcriptionId}/speakers`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', ...getAuthHeaders() },
        body: JSON.stringify({
          mappings: mappingsArray,
        }),
      });

      if (!response.ok) {
        throw new Error(`Failed to save speaker mappings: ${response.statusText}`);
      }

      const updatedMappings: SpeakerMapping[] = await response.json();
      onSpeakerMappingsUpdate(updatedMappings);
      onOpenChange(false);
    } catch (err) {
      console.error('Error saving speaker mappings:', err);
      setError(err instanceof Error ? err.message : 'Failed to save speaker mappings');
    } finally {
      setIsSaving(false);
    }
  };

  const speakers = Object.keys(speakerMappings).sort();

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Users className="h-5 w-5" />
            Rename Speakers
          </DialogTitle>
        </DialogHeader>

        {isLoading ? (
          <div className="flex items-center justify-center py-8">
            <Loader2 className="h-6 w-6 animate-spin" />
            <span className="ml-2 text-sm text-muted-foreground">Loading speakers...</span>
          </div>
        ) : (
          <div className="space-y-4">
            {error && (
              <div className="p-3 rounded-md bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800">
                <p className="text-sm text-red-600 dark:text-red-400">{error}</p>
              </div>
            )}

            {enrollSuccess && (
              <div className="p-3 rounded-md bg-green-50 dark:bg-green-900/20 border border-green-200 dark:border-green-800">
                <p className="text-sm text-green-600 dark:text-green-400">{enrollSuccess}</p>
              </div>
            )}

            {speakers.length === 0 ? (
              <Card>
                <CardContent className="pt-6 text-center text-muted-foreground">
                  <Users className="h-8 w-8 mx-auto mb-2 opacity-50" />
                  <p>No speakers found with diarization enabled.</p>
                </CardContent>
              </Card>
            ) : (
              <div className="space-y-3 max-h-60 overflow-y-auto">
                {speakers.map((speaker) => {
                  const provenance = provenanceBySpeaker[speaker];
                  const isAutoMatched = provenance?.source === 'auto';
                  return (
                    <div
                      key={speaker}
                      className="space-y-1"
                    >
                      <div className="flex items-center justify-between gap-2">
                        <Label htmlFor={`speaker-${speaker}`} className="text-xs font-medium text-muted-foreground flex items-center gap-2">
                          {speaker}
                          {isAutoMatched && (
                            <span className="inline-flex items-center gap-1 rounded-full bg-primary/10 text-primary px-2 py-0.5 text-[10px] font-medium">
                              <Sparkles className="h-3 w-3" />
                              Auto-matched
                              {typeof provenance?.confidence === 'number' && ` · ${Math.round(provenance.confidence * 100)}%`}
                            </span>
                          )}
                        </Label>
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          className="h-6 px-2 text-xs text-muted-foreground hover:text-foreground"
                          onClick={() => handleEnrollToggle(speaker)}
                          disabled={isEnrolling}
                        >
                          <Mic2 className="h-3 w-3 mr-1" />
                          Enroll voice
                        </Button>
                      </div>
                      <Input
                        id={`speaker-${speaker}`}
                        value={speakerMappings[speaker] || ''}
                        onChange={(e) => handleSpeakerNameChange(speaker, e.target.value)}
                        placeholder={`Enter custom name for ${speaker}`}
                        className="transition-all duration-200 focus:ring-2 focus:ring-primary/20"
                      />
                      {enrollTarget === speaker && (
                        <div className="mt-1 flex flex-col gap-2 rounded-md border border-border p-2">
                          <select
                            value={enrollProfileChoice}
                            onChange={(e) => setEnrollProfileChoice(e.target.value)}
                            disabled={isEnrolling}
                            className="h-8 rounded-md border border-input bg-background px-2 text-xs"
                            aria-label="Voice profile to enroll into"
                          >
                            {voiceProfiles.map(profile => (
                              <option key={profile.id} value={String(profile.id)}>
                                {profile.name} ({profile.sample_count} {profile.sample_count === 1 ? 'sample' : 'samples'})
                              </option>
                            ))}
                            <option value={NEW_PROFILE_OPTION}>New voice...</option>
                          </select>
                          {enrollProfileChoice === NEW_PROFILE_OPTION && (
                            <Input
                              value={newProfileName}
                              onChange={(e) => setNewProfileName(e.target.value)}
                              placeholder="Name for the new voice"
                              className="h-8 text-xs"
                              disabled={isEnrolling}
                            />
                          )}
                          <Button
                            type="button"
                            size="sm"
                            className="h-8"
                            onClick={() => handleEnroll(speaker)}
                            disabled={isEnrolling}
                          >
                            {isEnrolling ? (
                              <>
                                <Loader2 className="h-3 w-3 mr-1 animate-spin" />
                                Extracting voice...
                              </>
                            ) : (
                              'Enroll this voice'
                            )}
                          </Button>
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        )}

        <DialogFooter className="gap-2">
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={isSaving}>
            <X className="h-4 w-4 mr-1" />
            Cancel
          </Button>
          <Button
            onClick={saveSpeakerMappings}
            disabled={isSaving || speakers.length === 0}
            className="min-w-[100px]"
          >
            {isSaving ? (
              <>
                <Loader2 className="h-4 w-4 mr-1 animate-spin" />
                Saving...
              </>
            ) : (
              <>
                <Save className="h-4 w-4 mr-1" />
                Save
              </>
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
};

export default SpeakerRenameDialog;
