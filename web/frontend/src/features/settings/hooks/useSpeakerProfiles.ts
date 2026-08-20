import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@/features/auth/hooks/useAuth";

export interface SpeakerProfile {
	id: number;
	name: string;
	notes: string;
	sample_count: number;
	updated_at: string;
}

export interface SpeakerProfileSample {
	id: number;
	source: string;
	source_speaker_label: string;
	seconds_used: number;
	created_at: string;
}

interface SpeakerProfileUpsertPayload {
	name: string;
	notes?: string;
}

async function parseErrorMessage(response: Response, fallback: string): Promise<string> {
	try {
		const data = await response.json();
		if (data && typeof data.error === "string" && data.error.trim()) {
			return data.error;
		}
	} catch {
		// Response body was not JSON (e.g. the backend is unreachable); fall
		// through to the generic message.
	}
	return fallback;
}

export function useSpeakerProfiles() {
	const { getAuthHeaders } = useAuth();

	return useQuery({
		queryKey: ["speakerProfiles"],
		queryFn: async (): Promise<SpeakerProfile[]> => {
			const response = await fetch("/api/v1/speakers/profiles", {
				headers: getAuthHeaders(),
			});
			if (!response.ok) {
				throw new Error(await parseErrorMessage(response, "Failed to load voice profiles."));
			}
			return response.json();
		},
	});
}

export function useCreateSpeakerProfile() {
	const { getAuthHeaders } = useAuth();
	const queryClient = useQueryClient();

	return useMutation({
		mutationFn: async (payload: SpeakerProfileUpsertPayload): Promise<SpeakerProfile> => {
			const response = await fetch("/api/v1/speakers/profiles", {
				method: "POST",
				headers: { "Content-Type": "application/json", ...getAuthHeaders() },
				body: JSON.stringify(payload),
			});
			if (!response.ok) {
				throw new Error(await parseErrorMessage(response, "Failed to create voice profile."));
			}
			return response.json();
		},
		onSuccess: () => {
			queryClient.invalidateQueries({ queryKey: ["speakerProfiles"] });
		},
	});
}

export function useUpdateSpeakerProfile() {
	const { getAuthHeaders } = useAuth();
	const queryClient = useQueryClient();

	return useMutation({
		mutationFn: async ({
			id,
			...payload
		}: SpeakerProfileUpsertPayload & { id: number }): Promise<SpeakerProfile> => {
			const response = await fetch(`/api/v1/speakers/profiles/${id}`, {
				method: "PATCH",
				headers: { "Content-Type": "application/json", ...getAuthHeaders() },
				body: JSON.stringify(payload),
			});
			if (!response.ok) {
				throw new Error(await parseErrorMessage(response, "Failed to update voice profile."));
			}
			return response.json();
		},
		onSuccess: () => {
			queryClient.invalidateQueries({ queryKey: ["speakerProfiles"] });
		},
	});
}

export function useDeleteSpeakerProfile() {
	const { getAuthHeaders } = useAuth();
	const queryClient = useQueryClient();

	return useMutation({
		mutationFn: async (id: number): Promise<void> => {
			const response = await fetch(`/api/v1/speakers/profiles/${id}`, {
				method: "DELETE",
				headers: getAuthHeaders(),
			});
			if (!response.ok) {
				throw new Error(await parseErrorMessage(response, "Failed to delete voice profile."));
			}
		},
		onSuccess: () => {
			queryClient.invalidateQueries({ queryKey: ["speakerProfiles"] });
		},
	});
}

export function useSpeakerProfileSamples(profileId: number, enabled: boolean) {
	const { getAuthHeaders } = useAuth();

	return useQuery({
		queryKey: ["speakerProfileSamples", profileId],
		queryFn: async (): Promise<SpeakerProfileSample[]> => {
			const response = await fetch(`/api/v1/speakers/profiles/${profileId}/samples`, {
				headers: getAuthHeaders(),
			});
			if (!response.ok) {
				throw new Error(await parseErrorMessage(response, "Failed to load voice samples."));
			}
			return response.json();
		},
		enabled,
	});
}

export function useDeleteSpeakerProfileSample(profileId: number) {
	const { getAuthHeaders } = useAuth();
	const queryClient = useQueryClient();

	return useMutation({
		mutationFn: async (sampleId: number): Promise<void> => {
			const response = await fetch(`/api/v1/speakers/profiles/${profileId}/samples/${sampleId}`, {
				method: "DELETE",
				headers: getAuthHeaders(),
			});
			if (!response.ok) {
				throw new Error(await parseErrorMessage(response, "Failed to delete voice sample."));
			}
		},
		onSuccess: () => {
			queryClient.invalidateQueries({ queryKey: ["speakerProfileSamples", profileId] });
			queryClient.invalidateQueries({ queryKey: ["speakerProfiles"] });
		},
	});
}
