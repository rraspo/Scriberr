import { useState } from "react";
import { cn } from "@/lib/utils";

const COLLAPSED_LINES = 4;
const COLLAPSED_CHARACTERS = 280;

// The post caption saved with a link import: line breaks kept, long captions
// folded to four lines with a toggle.
export function SourceCaption({ caption }: { caption: string }) {
    const [expanded, setExpanded] = useState(false);
    const isLong = caption.split("\n").length > COLLAPSED_LINES || caption.length > COLLAPSED_CHARACTERS;

    return (
        <div className="max-w-prose text-sm text-[var(--text-secondary)]">
            <p className={cn("whitespace-pre-line break-words", isLong && !expanded && "line-clamp-4")}>{caption}</p>
            {isLong && (
                <button
                    type="button"
                    onClick={() => setExpanded((value) => !value)}
                    aria-expanded={expanded}
                    className="mt-1 text-xs font-medium text-[var(--text-tertiary)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                >
                    {expanded ? "Show less" : "Show more"}
                </button>
            )}
        </div>
    );
}
