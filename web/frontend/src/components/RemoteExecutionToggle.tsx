import { useCallback, useEffect, useState, useSyncExternalStore } from "react";
import { RefreshCw } from "lucide-react";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";
import { useAuth } from "@/features/auth/hooks/useAuth";
import {
	getRemoteExecutionSnapshot,
	setRemoteExecutionHealth,
	setRemoteExecutionPreference,
	subscribeRemoteExecution,
	type RemoteExecutionHealth,
} from "@/lib/remoteExecution";

const REMOTE_HEALTH_URL = "/api/v1/transcription/remote-execution/health";
const REMOTE_HEALTH_CHECK_URL = "/api/v1/transcription/remote-execution/health/check";

function parseHealthResponse(data: Record<string, unknown>): RemoteExecutionHealth {
	return {
		hasRemote: Boolean(data.has_remote),
		known: Boolean(data.known),
		reachable: Boolean(data.reachable),
		source: typeof data.source === "string" ? data.source : "",
		checkedAt: typeof data.checked_at === "string" ? data.checked_at : null,
	};
}

function describeAge(checkedAt: string | null): string {
	if (!checkedAt) return "";
	const elapsedMs = Date.now() - new Date(checkedAt).getTime();
	if (elapsedMs < 60_000) return "just now";
	const minutes = Math.floor(elapsedMs / 60_000);
	if (minutes < 60) return `${minutes}m ago`;
	const hours = Math.floor(minutes / 60);
	if (hours < 24) return `${hours}h ago`;
	return `${Math.floor(hours / 24)}d ago`;
}

export function RemoteExecutionToggle() {
	const { getAuthHeaders } = useAuth();
	const [isChecking, setIsChecking] = useState(false);
	const { health, preferenceEnabled } = useSyncExternalStore(
		subscribeRemoteExecution,
		getRemoteExecutionSnapshot,
		getRemoteExecutionSnapshot
	);

	// One passive read on mount. The backend serves its last observation and
	// never contacts the GPU host for this request, so there is nothing to
	// poll: new observations arrive from real job runs or the manual check.
	const fetchHealth = useCallback(async () => {
		try {
			const response = await fetch(REMOTE_HEALTH_URL, {
				headers: { ...getAuthHeaders() },
			});
			if (!response.ok) return;
			setRemoteExecutionHealth(parseHealthResponse(await response.json()));
		} catch (error) {
			console.error("Failed to fetch remote execution health:", error);
		}
	}, [getAuthHeaders]);

	const checkNow = useCallback(async () => {
		setIsChecking(true);
		try {
			const response = await fetch(REMOTE_HEALTH_CHECK_URL, {
				method: "POST",
				headers: { ...getAuthHeaders() },
			});
			if (!response.ok) return;
			setRemoteExecutionHealth(parseHealthResponse(await response.json()));
		} catch (error) {
			console.error("Failed to check remote execution health:", error);
		} finally {
			setIsChecking(false);
		}
	}, [getAuthHeaders]);

	useEffect(() => {
		fetchHealth();
	}, [fetchHealth]);

	if (!health || !health.hasRemote) {
		return null;
	}

	const { known, reachable, checkedAt, source } = health;
	const age = describeAge(checkedAt);
	const statusText = !known
		? "GPU host status unknown — jobs still try the GPU first and fall back to local on their own. Check now to probe once."
		: reachable
			? `GPU host reachable (${source === "job" ? "last job" : "checked"} ${age}). Jobs run there when enabled.`
			: `GPU host was unreachable ${age} (${source === "job" ? "last job fell back to local" : "manual check"}). Jobs still try it first and fall back to local.`;

	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<div
					className={cn(
						"flex items-center gap-2 px-3 py-1.5 rounded-full",
						"border border-[var(--border-subtle)] bg-[var(--bg-card)] shadow-sm",
						"transition-opacity motion-reduce:transition-none",
						known && !reachable && "opacity-70"
					)}
				>
					<span
						className={cn(
							"h-2 w-2 rounded-full transition-colors motion-reduce:transition-none",
							!known ? "bg-zinc-400" : reachable ? "bg-emerald-500" : "bg-red-400"
						)}
						aria-hidden="true"
					/>
					<span className="text-sm font-medium text-[var(--text-primary)] whitespace-nowrap">
						Remote GPU
					</span>
					{known && age && (
						<span className="text-xs text-[var(--text-tertiary)] whitespace-nowrap">{age}</span>
					)}
					<button
						type="button"
						onClick={checkNow}
						disabled={isChecking}
						aria-label="Check GPU host reachability now"
						className="text-[var(--text-tertiary)] hover:text-[var(--text-primary)] disabled:opacity-50"
					>
						<RefreshCw className={cn("h-3.5 w-3.5", isChecking && "animate-spin motion-reduce:animate-none")} />
					</button>
					<Switch
						checked={preferenceEnabled}
						onCheckedChange={setRemoteExecutionPreference}
						aria-label="Toggle remote GPU execution"
					/>
				</div>
			</TooltipTrigger>
			<TooltipContent>{statusText}</TooltipContent>
		</Tooltip>
	);
}
