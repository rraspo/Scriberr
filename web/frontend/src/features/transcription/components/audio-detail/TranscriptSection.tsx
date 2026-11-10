import { useCallback } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { TranscriptView } from "@/components/transcript/TranscriptView";
import SpeakerRenameDialog from "./SpeakerRenameDialog";
import { useIsDesktop } from "@/hooks/useIsDesktop";
import { useTranscriptSeekHint } from "@/hooks/use-transcript-seek-hint";
import { X, Hand } from "lucide-react";
import type { Transcript } from "@/features/transcription/hooks/useAudioDetail";
import { cn } from "@/lib/utils";

interface TranscriptSectionProps {
    audioId: string;
    currentTime: number;
    isPlaying: boolean;
    onSeek: (time: number) => void;
    transcript: Transcript | undefined;
    speakerMappings: Record<string, string>;
    transcriptMode: 'compact' | 'expanded';
    autoScrollEnabled: boolean;
    speakerRenameOpen: boolean;
    setSpeakerRenameOpen: (open: boolean) => void;
}

export function TranscriptSection({
    audioId,
    currentTime,
    isPlaying,
    onSeek,
    transcript,
    speakerMappings,
    transcriptMode,
    autoScrollEnabled,
    speakerRenameOpen,
    setSpeakerRenameOpen,
    className
}: TranscriptSectionProps & { className?: string }) {
    const isDesktop = useIsDesktop();
    const queryClient = useQueryClient();

    // Click-to-play discoverability hint. Desktop communicates clickability through the
    // hover cursor in TranscriptView; touch devices have no hover state, so they get an
    // explicit, dismissible, first-visit hint instead.
    const { shouldShowHint, markHintShown } = useTranscriptSeekHint();
    const handleSeek = useCallback((time: number) => {
        markHintShown();
        onSeek(time);
    }, [markHintShown, onSeek]);

    const getDetectedSpeakers = () => {
        if (!transcript?.segments) return [];
        const speakers = new Set<string>();
        transcript.segments.forEach((segment) => {
            if (segment.speaker) speakers.add(segment.speaker);
        });
        return Array.from(speakers).sort();
    };

    if (!transcript) return null;

    return (
        <div className="md:glass-card md:rounded-[var(--radius-card)] md:border-[var(--border-subtle)] md:shadow-[var(--shadow-card)] md:hover:shadow-[var(--shadow-float)] p-4 md:p-6 min-h-[500px] transition-shadow">
            <div className={cn("relative font-sans", className)}>
                {!isDesktop && shouldShowHint && (
                    <div className="mb-3 flex items-center justify-between gap-3 rounded-[var(--radius-btn)] border border-[var(--border-subtle)] bg-[var(--brand-light)] px-4 py-3 text-sm text-[var(--text-secondary)]">
                        <div className="flex items-center gap-2">
                            <Hand className="h-4 w-4 flex-shrink-0 text-[var(--brand-solid)]" />
                            <span>Tap a word to play from that point.</span>
                        </div>
                        <button
                            type="button"
                            onClick={markHintShown}
                            className="flex-shrink-0 text-[var(--text-tertiary)] hover:text-[var(--text-primary)] transition-colors"
                            aria-label="Dismiss hint"
                        >
                            <X className="h-4 w-4" />
                        </button>
                    </div>
                )}
                <div className="w-full text-[var(--text-secondary)] leading-relaxed">
                    <TranscriptView
                        transcript={transcript}
                        mode={transcriptMode}
                        currentTime={currentTime}
                        isPlaying={isPlaying}
                        onSeek={handleSeek}
                        speakerMappings={speakerMappings}
                        autoScrollEnabled={autoScrollEnabled}
                    />
                </div>
            </div>

            <SpeakerRenameDialog
                open={speakerRenameOpen}
                onOpenChange={setSpeakerRenameOpen}
                transcriptionId={audioId}
                initialSpeakers={getDetectedSpeakers()}
                onSpeakerMappingsUpdate={() => {
                    queryClient.invalidateQueries({ queryKey: ["speakerMappings", audioId] });
                }}
            />
        </div>
    );
}
