import { useQuery } from "@tanstack/react-query";
import { useAuth } from "@/features/auth/hooks/useAuth";

export interface JobSheet {
    index: number;
    start_seconds: number;
    end_seconds: number;
    frame_count: number;
    url: string;
    content_type: string;
}

export interface JobFrame {
    index: number;
    timestamp_seconds: number;
    url: string;
    label?: string;
}

export interface JobVideo {
    url: string;
    content_type: string;
    size_bytes: number;
    duration_seconds: number;
}

export interface JobVisuals {
    status: "none" | "pending" | "ready" | "failed";
    error: string | null;
    sheets: JobSheet[];
    frames?: JobFrame[];
    video: JobVideo | null;
}

// A job's contact sheets and kept video. Polls while the frames are building.
export function useJobVisuals(jobId: string, visualsStatus?: string) {
    const { getAuthHeaders } = useAuth();

    return useQuery({
        queryKey: ["visuals", jobId],
        enabled: Boolean(visualsStatus),
        queryFn: async () => {
            const response = await fetch(`/api/v1/transcription/${jobId}/visuals`, {
                headers: getAuthHeaders(),
            });
            if (!response.ok) throw new Error("Failed to fetch frames");
            return response.json() as Promise<JobVisuals>;
        },
        refetchInterval: (query) => (query.state.data?.status === "pending" ? 3000 : false),
    });
}
