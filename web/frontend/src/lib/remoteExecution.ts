// Tiny external store for remote-execution health and the user's toggle
// preference. Designed for React's useSyncExternalStore.
//
// Health is a last-known observation reported by the backend (from a real job
// run or an explicit check), never a live probe: unsolicited probing can wake
// a sleeping wake-on-LAN GPU host. "Unknown" is therefore a normal state, and
// job routing must not treat it as unreachable - the backend falls back to
// local execution on its own when the remote host is actually down.

export interface RemoteExecutionHealth {
	hasRemote: boolean;
	known: boolean;
	reachable: boolean;
	source: string;
	checkedAt: string | null;
}

interface RemoteExecutionState {
	health: RemoteExecutionHealth | null;
	preferenceEnabled: boolean;
}

const PREFERENCE_STORAGE_KEY = "scriberr.remoteExecutionEnabled";

function readStoredPreference(): boolean {
	if (typeof window === "undefined") {
		return true;
	}

	const stored = window.localStorage.getItem(PREFERENCE_STORAGE_KEY);
	if (stored === null) {
		return true;
	}

	return stored === "true";
}

let state: RemoteExecutionState = {
	health: null,
	preferenceEnabled: readStoredPreference(),
};

const listeners = new Set<() => void>();

function emitChange(): void {
	listeners.forEach((listener) => listener());
}

export function subscribeRemoteExecution(listener: () => void): () => void {
	listeners.add(listener);
	return () => {
		listeners.delete(listener);
	};
}

export function getRemoteExecutionSnapshot(): RemoteExecutionState {
	return state;
}

export function setRemoteExecutionHealth(health: RemoteExecutionHealth): void {
	state = { ...state, health };
	emitChange();
}

export function setRemoteExecutionPreference(enabled: boolean): void {
	if (typeof window !== "undefined") {
		window.localStorage.setItem(PREFERENCE_STORAGE_KEY, String(enabled));
	}
	state = { ...state, preferenceEnabled: enabled };
	emitChange();
}

export function buildStartTranscriptionUrl(jobId: string, profileId?: string): string {
	const queryParts: string[] = [];

	if (profileId) {
		queryParts.push(`profile_id=${encodeURIComponent(profileId)}`);
	}

	// Only an explicit user preference forces local execution. Last-known
	// reachability does not gate routing: the backend's own fallback handles
	// an unreachable host, and the attempt refreshes the health observation.
	if (!state.preferenceEnabled) {
		queryParts.push("execution_mode=local");
	}

	const baseUrl = `/api/v1/transcription/${encodeURIComponent(jobId)}/start`;
	return queryParts.length > 0 ? `${baseUrl}?${queryParts.join("&")}` : baseUrl;
}
