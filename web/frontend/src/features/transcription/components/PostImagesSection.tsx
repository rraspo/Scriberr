import { Images, Loader2 } from "lucide-react";
import { useJobVisuals } from "@/features/transcription/hooks/useJobVisuals";

// The images of an Instagram post, in order, each opening full size in a new tab.
export function PostImagesSection({ jobId, visualsStatus }: { jobId: string; visualsStatus?: string }) {
    const { data } = useJobVisuals(jobId, visualsStatus);
    const status = data?.status ?? visualsStatus;
    const frames = data?.frames ?? [];

    if (status === "pending") {
        return (
            <p className="flex items-center gap-2 text-sm text-[var(--text-secondary)]">
                <Loader2 className="h-4 w-4 animate-spin" />
                Loading images…
            </p>
        );
    }
    if (status === "failed") {
        return data?.error ? <p className="text-sm text-[var(--text-tertiary)] break-words">{data.error}</p> : null;
    }
    if (status !== "ready" || frames.length === 0) return null;

    return (
        <section className="min-w-0 space-y-2" aria-label="Images">
            <h2 className="flex items-center gap-2 text-sm font-semibold text-[var(--text-primary)]">
                <Images className="h-4 w-4 text-[var(--text-tertiary)]" />
                Images
            </h2>
            <ul className="grid grid-cols-2 gap-3 sm:grid-cols-3">
                {frames.map((frame, position) => (
                    <li key={frame.index} className="min-w-0">
                        <a
                            href={frame.url}
                            target="_blank"
                            rel="noopener noreferrer"
                            className="block rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--brand-solid)]"
                        >
                            <div className="flex aspect-square items-center justify-center overflow-hidden rounded-md border border-[var(--border-subtle)] bg-[var(--bg-card)]">
                                <img
                                    src={frame.url}
                                    alt={`Image ${position + 1} of ${frames.length}`}
                                    loading="lazy"
                                    className="h-full w-full object-contain"
                                />
                            </div>
                            {frame.label && (
                                <span className="mt-1 block text-xs tabular-nums text-[var(--text-tertiary)]">{frame.label}</span>
                            )}
                        </a>
                    </li>
                ))}
            </ul>
        </section>
    );
}
