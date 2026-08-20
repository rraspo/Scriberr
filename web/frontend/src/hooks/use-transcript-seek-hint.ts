import { useEffect, useState } from 'react';

const STORAGE_KEY = 'scriberr_transcript_seek_hint_shown';

/**
 * Hook to manage the first-visit "tap a word to seek" discovery hint shown to
 * touch users, who have no hover state to reveal that the transcript is clickable.
 * Mirrors the shape of useSwipeHint.
 */
export function useTranscriptSeekHint() {
    const [shouldShowHint, setShouldShowHint] = useState(false);

    useEffect(() => {
        const hasShown = localStorage.getItem(STORAGE_KEY);
        if (!hasShown) {
            setShouldShowHint(true);
        }
    }, []);

    const markHintShown = () => {
        localStorage.setItem(STORAGE_KEY, 'true');
        setShouldShowHint(false);
    };

    return { shouldShowHint, markHintShown };
}
