import { Film, Loader2, Video } from "lucide-react";
import { useJobVisuals } from "@/features/transcription/hooks/useJobVisuals";

function formatTime(totalSeconds: number): string {
    const whole = Math.floor(totalSeconds);
    const hours = Math.floor(whole / 3600);
    const minutes = Math.floor((whole % 3600) / 60);
    const seconds = String(whole % 60).padStart(2, "0");
    return hours > 0 ? `${hours}:${String(minutes).padStart(2, "0")}:${seconds}` : `${minutes}:${seconds}`;
}

// The frames of a link import's video: a strip of contact sheets, each opening
// full size in a new tab, plus a link to the kept video.
export function JobVisualsSection({ jobId, visualsStatus }: { jobId: string; visualsStatus?: string }) {
    const { data } = useJobVisuals(jobId, visualsStatus);
    const status = data?.status ?? visualsStatus;

    if (status === "pending") {
        return (
            <p className="flex items-center gap-2 text-sm text-[var(--text-secondary)]">
                <Loader2 className="h-4 w-4 animate-spin" />
                Building frames…
            </p>
        );
    }
    if (status === "failed") {
        return data?.error ? <p className="text-sm text-[var(--text-tertiary)] break-words">{data.error}</p> : null;
    }
    if (status !== "ready" || !data || data.sheets.length === 0) return null;

    return (
        <section className="min-w-0 space-y-2" aria-label="Frames">
            <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
                <h2 className="flex items-center gap-2 text-sm font-semibold text-[var(--text-primary)]">
                    <Film className="h-4 w-4 text-[var(--text-tertiary)]" />
                    Frames
                </h2>
                {data.video && (
                    <a
                        href={data.video.url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="inline-flex items-center gap-1 text-sm text-[var(--text-tertiary)] underline-offset-2 hover:underline hover:text-primary transition-colors"
                    >
                        <Video className="h-3.5 w-3.5" />
                        Video
                    </a>
                )}
            </div>
            <ul className="flex max-w-full gap-3 overflow-x-auto pb-2">
                {data.sheets.map((sheet) => (
                    <li key={sheet.index} className="shrink-0">
                        <a href={sheet.url} target="_blank" rel="noopener noreferrer" className="block">
                            <img
                                src={sheet.url}
                                alt={`Frames from ${formatTime(sheet.start_seconds)} to ${formatTime(sheet.end_seconds)}`}
                                loading="lazy"
                                className="h-40 w-auto rounded-md border border-[var(--border-subtle)] bg-black"
                            />
                            <span className="mt-1 block text-xs tabular-nums text-[var(--text-tertiary)]">
                                {formatTime(sheet.start_seconds)} – {formatTime(sheet.end_seconds)}
                            </span>
                        </a>
                    </li>
                ))}
            </ul>
        </section>
    );
}
