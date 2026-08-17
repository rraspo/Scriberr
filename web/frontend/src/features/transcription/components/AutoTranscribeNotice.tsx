import { useEffect, useState } from "react";
import { Info } from "lucide-react";
import { useAuth } from "@/features/auth/hooks/useAuth";

interface DefaultProfile {
	id: string;
	name: string;
	execution_mode: "local" | "remote";
	parameters: {
		model?: string;
		language?: string | null;
	};
}

// Uploads are auto-enqueued with the default profile's parameters, so the
// language is decided before the operator sees the job. Naming the profile up
// front is what makes an early cancel-and-reselect possible.
export function AutoTranscribeNotice() {
	const { getAuthHeaders } = useAuth();
	const [profile, setProfile] = useState<DefaultProfile | null>(null);

	useEffect(() => {
		let cancelled = false;

		const load = async () => {
			try {
				const response = await fetch("/api/v1/user/default-profile", {
					headers: { ...getAuthHeaders() },
				});
				if (!response.ok) {
					if (!cancelled) setProfile(null);
					return;
				}
				const data: DefaultProfile = await response.json();
				if (!cancelled) setProfile(data);
			} catch {
				if (!cancelled) setProfile(null);
			}
		};

		void load();
		return () => {
			cancelled = true;
		};
	}, [getAuthHeaders]);

	if (!profile) return null;

	const language = profile.parameters?.language?.trim();
	const model = profile.parameters?.model;
	const where = profile.execution_mode === "remote" ? "GPU" : "CPU";

	const details = [
		language ? language.toUpperCase() : "auto-detected language",
		model,
		where,
	]
		.filter(Boolean)
		.join(" · ");

	return (
		<div className="mb-4 flex items-start gap-2.5 rounded-[var(--radius-btn)] border border-[var(--border-subtle)] bg-[var(--bg-card)] px-3.5 py-2.5">
			<Info className="mt-0.5 h-3.5 w-3.5 flex-shrink-0 text-[var(--text-tertiary)]" />
			<p className="text-xs leading-relaxed text-[var(--text-secondary)]">
				Uploaded files start transcribing automatically with{" "}
				<span className="font-medium text-[var(--text-primary)]">{profile.name}</span>
				<span className="text-[var(--text-tertiary)]"> ({details})</span>. To use a different
				one, cancel the job and start it again with that profile.
			</p>
		</div>
	);
}
