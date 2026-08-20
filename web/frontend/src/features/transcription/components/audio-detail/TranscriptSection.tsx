import { useRef, useCallback, useMemo } from "react";
import { createPortal } from "react-dom";
import { useQueryClient } from "@tanstack/react-query";
import { TranscriptView } from "@/components/transcript/TranscriptView";
import { useNotes, useCreateNote, useUpdateNote, useDeleteNote } from "@/features/transcription/hooks/useTranscriptionNotes";
import { useSelectionMenu } from "@/features/transcription/hooks/useSelectionMenu";
import { DownloadDialog } from "./DownloadDialog";
import SpeakerRenameDialog from "./SpeakerRenameDialog";
import { NotesSidebar } from "./NotesSidebar";
import { TranscriptSelectionMenu } from "./TranscriptSelectionMenu";
import { NoteEditorDialog } from "./NoteEditorDialog";
import { useIsMobile } from "@/hooks/use-mobile";
import { useIsDesktop } from "@/hooks/useIsDesktop";
import { useTranscriptSeekHint } from "@/hooks/use-transcript-seek-hint";
import { X, StickyNote, Hand } from "lucide-react";
import { computeWordOffsets } from "@/features/transcription/hooks/useKaraokeHighlight";
import type { Transcript } from "@/features/transcription/hooks/useAudioDetail";
import { cn } from "@/lib/utils";

interface TranscriptSectionProps {
    audioId: string;
    currentTime: number;
    isPlaying: boolean;
    onSeek: (time: number) => void;
    // Lifted State Props
    transcript: Transcript | undefined;
    speakerMappings: Record<string, string>;
    transcriptMode: 'compact' | 'expanded';
    autoScrollEnabled: boolean;
    notesOpen: boolean;
    setNotesOpen: (open: boolean) => void;
    speakerRenameOpen: boolean;
    setSpeakerRenameOpen: (open: boolean) => void;
    downloadDialogOpen: boolean;
    setDownloadDialogOpen: (open: boolean) => void;
    downloadFormat: 'txt' | 'json';
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
    notesOpen,
    setNotesOpen,
    speakerRenameOpen,
    setSpeakerRenameOpen,
    downloadDialogOpen,
    setDownloadDialogOpen,
    downloadFormat,
    className
}: TranscriptSectionProps & { className?: string }) {
    const isMobile = useIsMobile();
    const isDesktop = useIsDesktop();
    const queryClient = useQueryClient();

    // Data hooks
    const { data: notes = [] } = useNotes(audioId);
    const { mutate: createNote } = useCreateNote(audioId);
    const { mutateAsync: updateNote } = useUpdateNote(audioId);
    const { mutateAsync: deleteNote } = useDeleteNote(audioId);

    // Refs
    const transcriptRef = useRef<HTMLDivElement>(null);

    // Compute offsets for selection logic
    const words = useMemo(() => transcript?.word_segments || [], [transcript?.word_segments]);
    const { offsets } = useMemo(() => computeWordOffsets(words), [words]);

    // Unified Selection Hook for both desktop and mobile
    const {
        menuState,
        showEditor,
        openEditor,
        closeEditor
    } = useSelectionMenu(transcriptRef, offsets);

    // Click-to-seek discoverability hint. Desktop communicates clickability through the
    // hover cursor in TranscriptView; touch devices have no hover state, so they get an
    // explicit, dismissible, first-visit hint instead.
    const { shouldShowHint, markHintShown } = useTranscriptSeekHint();
    const handleSeek = useCallback((time: number) => {
        markHintShown();
        onSeek(time);
    }, [markHintShown, onSeek]);

    // Helpers
    const getDetectedSpeakers = () => {
        // Safe check for segments array
        if (!transcript?.segments) return [];
        const speakers = new Set<string>();
        // Using any for segment here if strict typing is an issue, or typed via Transcript
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        transcript.segments.forEach((segment: any) => {
            if (segment.speaker) speakers.add(segment.speaker);
        });
        return Array.from(speakers).sort();
    };

    const handleSaveNote = (content: string) => {
        if (menuState) {
            createNote({
                start_time: menuState.startTime,
                end_time: menuState.endTime,
                content: content,
                quote: menuState.selectedText,
                start_word_index: menuState.startIdx,
                end_word_index: menuState.endIdx
            });
            closeEditor();
            setNotesOpen(true);
        }
    };

    const handleListenFromHere = () => {
        if (menuState) {
            onSeek(menuState.startTime);
            closeEditor();
        }
    };

    if (!transcript) return null;

    return (
        <div className="md:glass-card md:rounded-[var(--radius-card)] md:border-[var(--border-subtle)] md:shadow-[var(--shadow-card)] md:hover:shadow-[var(--shadow-float)] p-4 md:p-6 min-h-[500px] transition-shadow">
            {/* 
                  TOOLBAR REMOVED -> Moved to Context Menu 
                */}

            {/* Transcript Content - Systematic Typography */}
            <div
                className={cn("relative font-sans", className)}
                style={{
                    // Allow text selection to work in children
                    WebkitUserSelect: 'text',
                    userSelect: 'text'
                }}
            >
                {/* Touch discoverability hint: desktop shows clickability via hover, touch has
                    no hover state, so first-time touch visitors get an explicit callout. */}
                {!isDesktop && shouldShowHint && (
                    <div className="mb-3 flex items-center justify-between gap-3 rounded-[var(--radius-btn)] border border-[var(--border-subtle)] bg-[var(--brand-light)] px-4 py-3 text-sm text-[var(--text-secondary)]">
                        <div className="flex items-center gap-2">
                            <Hand className="h-4 w-4 flex-shrink-0 text-[var(--brand-solid)]" />
                            <span>Tap a word to jump to that point in the audio.</span>
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
                    <div
                        ref={transcriptRef}
                        className="relative"
                        style={{
                            // Ensure this container doesn't interfere with touch events
                            touchAction: 'pan-y pinch-zoom'
                        }}
                    >
                        <TranscriptView
                            transcript={transcript}
                            mode={transcriptMode}
                            currentTime={currentTime}
                            isPlaying={isPlaying}
                            notes={notes}
                            onSeek={handleSeek}
                            speakerMappings={speakerMappings}
                            autoScrollEnabled={autoScrollEnabled}
                        />
                    </div>
                </div>
            </div>

            {/* Download Dialog */}
            <DownloadDialog
                audioId={audioId}
                isOpen={downloadDialogOpen}
                onClose={setDownloadDialogOpen}
                initialFormat={downloadFormat}
            />

            {/* Speaker Rename Dialog */}
            <SpeakerRenameDialog
                open={speakerRenameOpen}
                onOpenChange={setSpeakerRenameOpen}
                transcriptionId={audioId}
                initialSpeakers={getDetectedSpeakers()}
                onSpeakerMappingsUpdate={() => {
                    queryClient.invalidateQueries({ queryKey: ["speakerMappings", audioId] });
                }}
            />
            {/* Portals */}
            {createPortal(
                <>
                    {/* Selection Menu Bubble (Glass) - Unified for desktop and mobile */}
                    {!showEditor && (
                        <TranscriptSelectionMenu
                            menuState={menuState}
                            onAddNote={openEditor}
                            onListenFromHere={handleListenFromHere}
                        />
                    )}

                    {/* Note Editor Dialog */}
                    <NoteEditorDialog
                        isOpen={showEditor}
                        quote={menuState?.selectedText || ""}
                        position={menuState ? { x: menuState.x, y: menuState.y } : { x: 0, y: 0 }}
                        onSave={handleSaveNote}
                        onCancel={closeEditor}
                    />

                    {/* Notes Sidebar - Premium Drawer */}
                    {notesOpen && (
                        <div className="fixed inset-y-0 right-0 w-[90vw] max-w-[400px] bg-[var(--bg-card)] border-l border-[var(--border-subtle)] shadow-[var(--shadow-float)] z-[9990] transition-transform duration-300 transform-gpu">
                            <div className="h-full flex flex-col">
                                <div className="px-6 py-5 border-b border-[var(--border-subtle)] flex items-center justify-between">
                                    <h3 className="font-bold text-[var(--text-primary)] flex items-center gap-2 text-lg">
                                        <StickyNote className="h-5 w-5 text-[var(--brand-solid)]" />
                                        Notes
                                        <span className="ml-1 text-xs font-bold rounded-full px-2 py-0.5 bg-[var(--bg-main)] text-[var(--text-secondary)] border border-[var(--border-subtle)]">
                                            {notes.length}
                                        </span>
                                    </h3>
                                    <button
                                        type="button"
                                        onClick={() => setNotesOpen(false)}
                                        className="h-8 w-8 inline-flex items-center justify-center rounded-[var(--radius-btn)] text-[var(--text-tertiary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-main)] transition-colors"
                                        aria-label="Close notes"
                                    >
                                        <X className="h-5 w-5" />
                                    </button>
                                </div>
                                <div className="flex-1 overflow-y-auto px-6 py-4">
                                    <NotesSidebar
                                        notes={notes}
                                        onEdit={(id, content) => updateNote({ id, content })}
                                        onDelete={(id) => deleteNote(id)}
                                        onJumpTo={(t) => {
                                            onSeek(t);
                                            if (isMobile) setNotesOpen(false);
                                        }}
                                    />
                                </div>
                            </div>
                        </div>
                    )}
                </>,
                document.body
            )}
        </div>
    );
}
