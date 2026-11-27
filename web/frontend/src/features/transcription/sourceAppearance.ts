import {
    CircleDot,
    FileAudio,
    Layers,
    MessageCircle,
    Mic,
    MonitorPlay,
    PhoneCall,
    Smartphone,
    Video,
    type LucideIcon,
} from "lucide-react";

export interface SourceAppearance {
    icon: LucideIcon;
    tintClasses: string;
}

const appearances: Record<string, SourceAppearance> = {
    youtube: { icon: MonitorPlay, tintClasses: "bg-red-50 text-red-600 dark:bg-red-950 dark:text-red-400" },
    plaud: { icon: Mic, tintClasses: "bg-violet-50 text-violet-600 dark:bg-violet-950 dark:text-violet-400" },
    call: { icon: PhoneCall, tintClasses: "bg-emerald-50 text-emerald-600 dark:bg-emerald-950 dark:text-emerald-400" },
    voice_memo: { icon: Smartphone, tintClasses: "bg-sky-50 text-sky-600 dark:bg-sky-950 dark:text-sky-400" },
    whatsapp: { icon: MessageCircle, tintClasses: "bg-green-50 text-green-600 dark:bg-green-950 dark:text-green-400" },
    omi: { icon: CircleDot, tintClasses: "bg-teal-50 text-teal-600 dark:bg-teal-950 dark:text-teal-400" },
    multitrack: { icon: Layers, tintClasses: "bg-amber-50 text-amber-600 dark:bg-amber-950 dark:text-amber-400" },
    video: { icon: Video, tintClasses: "bg-blue-50 text-blue-600 dark:bg-blue-950 dark:text-blue-400" },
    upload: { icon: FileAudio, tintClasses: "bg-[#FFFAF0] text-[#FF6D20]" },
};

// Unknown kinds fall back to a plain upload.
export function sourceAppearance(kind?: string): SourceAppearance {
    return (kind && appearances[kind]) || appearances.upload;
}
