import { useCallback, useEffect, useRef, useState } from "react";
import { ChevronDown, Loader2, Moon, Power } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { useToast } from "@/components/ui/toast";
import { useAuth } from "@/features/auth/hooks/useAuth";

type PowerAction = "wake" | "sleep";

const POWER_URL = "/api/v1/transcription/remote-execution/power";
const POLL_INTERVAL_MS = 2500;
const MAX_WAIT_MS = 4 * 60 * 1000;

const RUNNING_LABEL: Record<PowerAction, string> = {
	wake: "Waking GPU host…",
	sleep: "Putting GPU host to sleep…",
};
const SUCCESS_LABEL: Record<PowerAction, string> = {
	wake: "GPU host is up",
	sleep: "GPU host is asleep",
};

interface RemotePowerButtonsProps {
	reachable: boolean;
	onFinished: () => void;
}

async function readError(response: Response, fallback: string): Promise<string> {
	try {
		const data = await response.json();
		if (typeof data.error === "string" && data.error) return data.error;
	} catch {
		// Body was not JSON; use the fallback.
	}
	return fallback;
}

function wait(milliseconds: number): Promise<void> {
	return new Promise((resolve) => setTimeout(resolve, milliseconds));
}

export function RemotePowerButtons({ reachable, onFinished }: RemotePowerButtonsProps) {
	const { getAuthHeaders } = useAuth();
	const { toast } = useToast();
	const [runningAction, setRunningAction] = useState<PowerAction | null>(null);
	const [confirmSleepOpen, setConfirmSleepOpen] = useState(false);
	const [errorMessage, setErrorMessage] = useState<string | null>(null);
	const mountedRef = useRef(true);

	useEffect(() => {
		mountedRef.current = true;
		return () => {
			mountedRef.current = false;
		};
	}, []);

	const runAction = useCallback(
		async (action: PowerAction) => {
			setErrorMessage(null);
			setRunningAction(action);

			const fail = (message: string) => {
				if (!mountedRef.current) return;
				setErrorMessage(message);
				toast({ title: "GPU host power action failed", description: message });
			};

			try {
				const startResponse = await fetch(`${POWER_URL}/${action}`, {
					method: "POST",
					headers: { ...getAuthHeaders() },
				});
				if (!startResponse.ok) {
					fail(await readError(startResponse, `Could not start the ${action} action`));
					return;
				}
				const { run_id: runId } = await startResponse.json();
				if (!runId) {
					fail("The power service returned no run id");
					return;
				}

				const deadline = Date.now() + MAX_WAIT_MS;
				while (mountedRef.current && Date.now() < deadline) {
					await wait(POLL_INTERVAL_MS);
					if (!mountedRef.current) return;

					const statusResponse = await fetch(`${POWER_URL}/runs/${encodeURIComponent(runId)}`, {
						headers: { ...getAuthHeaders() },
					});
					if (!statusResponse.ok) {
						fail(await readError(statusResponse, "Could not read the power action status"));
						return;
					}
					const status = await statusResponse.json();
					if (!status.done) continue;

					if (status.ok) {
						toast({ title: SUCCESS_LABEL[action] });
					} else {
						fail(status.message || `The ${action} action failed`);
					}
					onFinished();
					return;
				}
				fail("Timed out waiting for the power action to finish");
			} catch (error) {
				console.error("GPU host power action failed:", error);
				fail("Could not reach the server. Try again.");
			} finally {
				if (mountedRef.current) setRunningAction(null);
			}
		},
		[getAuthHeaders, onFinished, toast]
	);

	const busy = runningAction !== null;

	return (
		<div className="flex items-center gap-2">
			{errorMessage && (
				<span role="alert" className="text-sm text-red-500">
					{errorMessage}
				</span>
			)}
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<Button variant="outline" size="sm" disabled={busy} className="rounded-full">
						{busy ? <Loader2 className="animate-spin motion-reduce:animate-none" /> : <Power />}
						{runningAction ? RUNNING_LABEL[runningAction] : "GPU host"}
						{!busy && <ChevronDown className="opacity-60" />}
					</Button>
				</DropdownMenuTrigger>
				<DropdownMenuContent
					align="end"
					className="w-48 glass-card border-[var(--border-subtle)] p-2 rounded-[var(--radius-card)] shadow-[var(--shadow-float)]"
				>
					<DropdownMenuItem
						disabled={reachable}
						onClick={() => runAction("wake")}
						className="cursor-pointer rounded-[var(--radius-btn)] focus:bg-[var(--secondary)] py-2.5"
					>
						<Power />
						Wake GPU host
					</DropdownMenuItem>
					<DropdownMenuItem
						disabled={!reachable}
						onClick={() => setConfirmSleepOpen(true)}
						className="cursor-pointer rounded-[var(--radius-btn)] focus:bg-[var(--secondary)] py-2.5"
					>
						<Moon />
						Sleep GPU host
					</DropdownMenuItem>
				</DropdownMenuContent>
			</DropdownMenu>

			<AlertDialog open={confirmSleepOpen} onOpenChange={setConfirmSleepOpen}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>Put the GPU host to sleep?</AlertDialogTitle>
						<AlertDialogDescription>
							Transcriptions will run locally until the GPU host is woken again.
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel>Cancel</AlertDialogCancel>
						<AlertDialogAction onClick={() => runAction("sleep")}>Sleep GPU host</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</div>
	);
}
