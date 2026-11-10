import { ChevronDown, type LucideIcon } from "lucide-react";
import type { ReactNode } from "react";

import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";

export interface SplitButtonItem {
    label: string;
    icon?: LucideIcon;
    onSelect: () => void;
    disabled?: boolean;
}

interface SplitButtonProps {
    /** The primary action's content: a label, with or without a leading icon. */
    children: ReactNode;
    /** Names the primary action and is its tooltip, for widths where only its icon shows. */
    primaryLabel: string;
    onClick: () => void;
    /** Accessible name of the chevron button and of the menu it opens. */
    menuLabel: string;
    items: SplitButtonItem[];
    /** Disables the primary action alone; the menu stays reachable. */
    primaryDisabled?: boolean;
    className?: string;
}

// Both parts keep a hairline on all four sides so the joined control has one
// height; the chevron overlaps the primary's inner edge by one hairline so the
// two inner edges read as a single divider, and the hovered or focused part
// rises above the other so its highlight is never cut off at the seam.
const PART =
    "relative inline-flex h-9 items-center justify-center border border-[var(--border-subtle)] bg-[var(--bg-card)] text-sm font-medium text-[var(--text-primary)] shadow-sm cursor-pointer select-none hover:z-10 hover:bg-[var(--bg-main)] focus-visible:z-10 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--brand-solid)] motion-safe:transition-colors disabled:cursor-not-allowed disabled:opacity-60";

/**
 * A main action joined to a chevron that opens a menu of related actions:
 * one control with one height and one outline. The menu is aligned to the
 * control's trailing edge.
 */
export function SplitButton({ children, primaryLabel, onClick, menuLabel, items, primaryDisabled = false, className }: SplitButtonProps) {
    return (
        <div className={cn("inline-flex items-stretch", className)}>
            <button type="button" onClick={onClick} disabled={primaryDisabled} aria-label={primaryLabel} title={primaryLabel} className={cn(PART, "gap-2 rounded-s-full px-3")}>
                {children}
            </button>
            <DropdownMenu>
                <DropdownMenuTrigger asChild>
                    <button type="button" aria-label={menuLabel} title={menuLabel} className={cn(PART, "-ms-px min-w-9 rounded-e-full px-2")}>
                        <ChevronDown className="h-4 w-4 text-[var(--text-secondary)]" aria-hidden="true" />
                    </button>
                </DropdownMenuTrigger>
                <DropdownMenuContent
                    align="end"
                    aria-label={menuLabel}
                    className="w-56 glass-card rounded-[var(--radius-card)] shadow-[var(--shadow-float)] border-[var(--border-subtle)] p-1.5"
                >
                    {items.map((item) => (
                        <DropdownMenuItem key={item.label} onClick={item.onSelect} disabled={item.disabled} className="rounded-[8px] cursor-pointer">
                            {item.icon ? <item.icon className="mr-2 h-4 w-4 opacity-70" aria-hidden="true" /> : null}
                            {item.label}
                        </DropdownMenuItem>
                    ))}
                </DropdownMenuContent>
            </DropdownMenu>
        </div>
    );
}
