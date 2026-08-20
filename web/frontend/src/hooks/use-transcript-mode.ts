import { useState, useCallback } from 'react';

const STORAGE_KEY = 'scriberr_transcript_mode';
type TranscriptMode = 'compact' | 'expanded';

/**
 * Hook to manage transcript view mode (compact or expanded/timeline) with localStorage persistence.
 * Default is expanded (Timeline View). User's explicit toggle choice persists across reloads.
 */
export function useTranscriptMode() {
    const [transcriptMode, setTranscriptModeState] = useState<TranscriptMode>(() => {
        const stored = localStorage.getItem(STORAGE_KEY);
        return (stored as TranscriptMode) || 'expanded';
    });

    const setTranscriptMode = useCallback((newMode: TranscriptMode | ((prev: TranscriptMode) => TranscriptMode)) => {
        setTranscriptModeState((prevMode) => {
            const mode = typeof newMode === 'function' ? newMode(prevMode) : newMode;
            localStorage.setItem(STORAGE_KEY, mode);
            return mode;
        });
    }, []);

    return [transcriptMode, setTranscriptMode] as const;
}
