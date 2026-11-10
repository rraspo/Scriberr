import type { Transcript } from "@/features/transcription/hooks/useAudioDetail";

const formatTimestamp = (seconds: number): string => {
    const minutes = Math.floor(seconds / 60);
    const remainingSeconds = Math.floor(seconds % 60);
    return `${minutes}:${remainingSeconds.toString().padStart(2, "0")}`;
};

const speakerName = (speaker: string, mappings: Record<string, string>) => mappings[speaker] || speaker;

export function formatTranscriptText(transcript: Transcript, speakerMappings: Record<string, string>): string {
    if (!transcript.segments) return transcript.text;
    return transcript.segments
        .map((segment) => {
            const label = segment.speaker ? `${speakerName(segment.speaker, speakerMappings)}: ` : '';
            return `[${formatTimestamp(segment.start)}] ${label}${segment.text.trim()}`;
        })
        .join('\n\n');
}

export function formatTranscriptJSON(transcript: Transcript, speakerMappings: Record<string, string>): string {
    const segments = (transcript.segments ?? []).map((segment) => ({
        start: segment.start,
        end: segment.end,
        timestamp: formatTimestamp(segment.start),
        ...(segment.speaker ? { speaker: speakerName(segment.speaker, speakerMappings) } : {}),
        text: segment.text.trim(),
    }));
    return JSON.stringify({ text: transcript.text, segments }, null, 2);
}

export function useTranscriptCopy() {
    const copyTXT = (transcript: Transcript, speakerMappings: Record<string, string>): Promise<void> =>
        navigator.clipboard.writeText(formatTranscriptText(transcript, speakerMappings));

    const copyJSON = (transcript: Transcript, speakerMappings: Record<string, string>): Promise<void> =>
        navigator.clipboard.writeText(formatTranscriptJSON(transcript, speakerMappings));

    return { copyTXT, copyJSON };
}
