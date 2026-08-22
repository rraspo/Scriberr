import { useCallback, useEffect, useSyncExternalStore } from "react";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";
import { useAuth } from "@/features/auth/hooks/useAuth";
import {
	getRemoteExecutionSnapshot,
	setRemoteExecutionHealth,
	setRemoteExecutionPreference,
	subscribeRemoteExecution,
} from "@/lib/remoteExecution";

const REMOTE_HEALTH_URL = "/api/v1/transcription/remote-execution/health";
const POLL_INTERVAL_MS = 20000;

export function RemoteExecutionToggle() {
	const { getAuthHeaders } = useAuth();
	const { health, preferenceEnabled } = useSyncExternalStore(
		subscribeRemoteExecution,
		getRemoteExecutionSnapshot,
		getRemoteExecutionSnapshot
	);

	const fetchHealth = useCallback(async () => {
		try {
			const response = await fetch(REMOTE_HEALTH_URL, {
				headers: { ...getAuthHeaders() },
			});
			if (!response.ok) return;

			const data = await response.json();
			setRemoteExecutionHealth({
				hasRemote: Boolean(data.has_remote),
				reachable: Boolean(data.reachable),
				hosts: Array.isArray(data.hosts) ? data.hosts : [],
			});
		} catch (error) {
			console.error("Failed to fetch remote execution health:", error);
		}
	}, [getAuthHeaders]);

	useEffect(() => {
		let intervalId: ReturnType<typeof setInterval> | undefined;

		const stopPolling = () => {
			if (intervalId !== undefined) {
				clearInterval(intervalId);
				intervalId = undefined;
			}
		};

		const startPolling = () => {
			if (intervalId !== undefined) return;
			intervalId = setInterval(() => {
				if (document.visibilityState === "visible") {
					fetchHealth();
				}
			}, POLL_INTERVAL_MS);
		};

		const handleVisibilityChange = () => {
			if (document.visibilityState === "visible") {
				fetchHealth();
				startPolling();
			} else {
				stopPolling();
			}
		};

		if (document.visibilityState === "visible") {
			fetchHealth();
			startPolling();
		}

		document.addEventListener("visibilitychange", handleVisibilityChange);

		return () => {
			document.removeEventListener("visibilitychange", handleVisibilityChange);
			stopPolling();
		};
	}, [fetchHealth]);

	if (!health || !health.hasRemote) {
		return null;
	}

	const reachable = health.reachable;

	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<div
					className={cn(
						"flex items-center gap-2 px-3 py-1.5 rounded-full",
						"border border-[var(--border-subtle)] bg-[var(--bg-card)] shadow-sm",
						"transition-opacity motion-reduce:transition-none",
						!reachable && "opacity-70"
					)}
				>
					<span
						className={cn(
							"h-2 w-2 rounded-full transition-colors motion-reduce:transition-none",
							reachable ? "bg-emerald-500" : "bg-red-400"
						)}
						aria-hidden="true"
					/>
					<span className="text-sm font-medium text-[var(--text-primary)] whitespace-nowrap">
						Remote GPU
					</span>
					<Switch
						checked={preferenceEnabled}
						disabled={!reachable}
						onCheckedChange={setRemoteExecutionPreference}
						aria-label="Toggle remote GPU execution"
					/>
				</div>
			</TooltipTrigger>
			<TooltipContent>
				{reachable
					? "Jobs run on the remote GPU host when enabled"
					: "GPU host unreachable — jobs will run locally"}
			</TooltipContent>
		</Tooltip>
	);
}
