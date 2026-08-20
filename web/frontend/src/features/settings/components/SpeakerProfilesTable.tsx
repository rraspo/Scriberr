import { Fragment, useCallback, useState } from "react";
import { ChevronDown, ChevronRight, Loader2, Mic2, Pencil, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "@/components/ui/table";
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
import {
	useDeleteSpeakerProfile,
	useDeleteSpeakerProfileSample,
	useSpeakerProfileSamples,
	useSpeakerProfiles,
	type SpeakerProfile,
	type SpeakerProfileSample,
} from "../hooks/useSpeakerProfiles";

interface SpeakerProfilesTableProps {
	onEditProfile: (profile: SpeakerProfile) => void;
	onCreateProfile: () => void;
	onMutationError: (message: string) => void;
	onMutationSuccess: (message: string) => void;
}

function formatDateTime(dateString: string) {
	return new Date(dateString).toLocaleDateString("en-US", {
		year: "numeric",
		month: "short",
		day: "numeric",
		hour: "2-digit",
		minute: "2-digit",
	});
}

function describeSampleSource(sample: SpeakerProfileSample) {
	const origin = sample.source === "correction" ? "Diarization correction" : "Enrollment";
	return sample.source_speaker_label ? `${origin} • speaker ${sample.source_speaker_label}` : origin;
}

interface SpeakerProfileSamplesPanelProps {
	profile: SpeakerProfile;
	onMutationError: (message: string) => void;
	onMutationSuccess: (message: string) => void;
}

function SpeakerProfileSamplesPanel({
	profile,
	onMutationError,
	onMutationSuccess,
}: SpeakerProfileSamplesPanelProps) {
	const { data: samples, isLoading, isError } = useSpeakerProfileSamples(profile.id, true);
	const deleteSampleMutation = useDeleteSpeakerProfileSample(profile.id);
	const [sampleToDelete, setSampleToDelete] = useState<SpeakerProfileSample | null>(null);

	const handleConfirmDeleteSample = useCallback(async () => {
		if (!sampleToDelete) return;
		const target = sampleToDelete;
		setSampleToDelete(null);
		try {
			await deleteSampleMutation.mutateAsync(target.id);
			onMutationSuccess(
				`Deleted the ${target.seconds_used.toFixed(1)}s sample (${describeSampleSource(target)}) from "${profile.name}".`,
			);
		} catch (error) {
			onMutationError(error instanceof Error ? error.message : "Failed to delete voice sample.");
		}
	}, [sampleToDelete, deleteSampleMutation, onMutationSuccess, onMutationError, profile.name]);

	if (isLoading) {
		return (
			<div className="flex items-center gap-2 text-sm text-[var(--text-secondary)] py-4">
				<Loader2 className="h-4 w-4 animate-spin" />
				Loading samples...
			</div>
		);
	}

	if (isError) {
		return (
			<div className="text-sm text-[var(--error)] py-4">
				Failed to load samples for "{profile.name}".
			</div>
		);
	}

	if (!samples || samples.length === 0) {
		return (
			<div className="text-sm text-[var(--text-secondary)] py-4">
				No samples enrolled for this voice yet.
			</div>
		);
	}

	return (
		<div className="py-2">
			<div className="space-y-2">
				{samples.map((sample) => (
					<div
						key={sample.id}
						className="flex items-center justify-between gap-3 bg-[var(--bg-card)] border border-[var(--border-subtle)] rounded-lg px-3 py-2"
					>
						<div className="min-w-0">
							<div className="text-sm font-medium text-[var(--text-primary)]">
								{describeSampleSource(sample)}
							</div>
							<div className="text-xs text-[var(--text-tertiary)] mt-0.5">
								{sample.seconds_used.toFixed(1)}s used &middot; enrolled {formatDateTime(sample.created_at)}
							</div>
						</div>
						<Button
							variant="ghost"
							size="sm"
							onClick={() => setSampleToDelete(sample)}
							disabled={deleteSampleMutation.isPending}
							aria-label="Delete sample"
							className="text-[var(--error)] hover:text-[var(--error)] hover:bg-[var(--error)]/10 shrink-0"
						>
							<Trash2 className="h-4 w-4" />
						</Button>
					</div>
				))}
			</div>

			<AlertDialog
				open={!!sampleToDelete}
				onOpenChange={(open) => {
					if (!open) setSampleToDelete(null);
				}}
			>
				<AlertDialogContent className="bg-[var(--bg-card)] border-[var(--border-subtle)]">
					<AlertDialogHeader>
						<AlertDialogTitle className="text-[var(--text-primary)]">Delete sample</AlertDialogTitle>
						<AlertDialogDescription className="text-[var(--text-secondary)]">
							{sampleToDelete && (
								<>
									Delete the {sampleToDelete.seconds_used.toFixed(1)}s sample from{" "}
									{describeSampleSource(sampleToDelete)}? This permanently destroys that voice
									embedding and cannot be undone.
								</>
							)}
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel className="bg-[var(--bg-secondary)] border-[var(--border-subtle)] text-[var(--text-primary)] hover:bg-[var(--bg-main)]">
							Cancel
						</AlertDialogCancel>
						<AlertDialogAction
							className="bg-[var(--error)] text-white hover:bg-[var(--error)]/90"
							onClick={handleConfirmDeleteSample}
						>
							Delete Sample
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</div>
	);
}

export function SpeakerProfilesTable({
	onEditProfile,
	onCreateProfile,
	onMutationError,
	onMutationSuccess,
}: SpeakerProfilesTableProps) {
	const { data: profiles, isLoading, isError } = useSpeakerProfiles();
	const deleteProfileMutation = useDeleteSpeakerProfile();
	const [expandedProfileId, setExpandedProfileId] = useState<number | null>(null);
	const [profileToDelete, setProfileToDelete] = useState<SpeakerProfile | null>(null);

	const toggleExpanded = useCallback((profileId: number) => {
		setExpandedProfileId((current) => (current === profileId ? null : profileId));
	}, []);

	const handleConfirmDeleteProfile = useCallback(async () => {
		if (!profileToDelete) return;
		const target = profileToDelete;
		setProfileToDelete(null);
		try {
			await deleteProfileMutation.mutateAsync(target.id);
			if (expandedProfileId === target.id) setExpandedProfileId(null);
			onMutationSuccess(
				`Deleted voice "${target.name}" and its ${target.sample_count} sample${target.sample_count === 1 ? "" : "s"}.`,
			);
		} catch (error) {
			onMutationError(error instanceof Error ? error.message : "Failed to delete voice profile.");
		}
	}, [profileToDelete, deleteProfileMutation, expandedProfileId, onMutationSuccess, onMutationError]);

	if (isLoading) {
		return (
			<div className="space-y-2">
				{[...Array(3)].map((_, i) => (
					<div
						key={i}
						className="bg-carbon-100 dark:bg-carbon-800 rounded-lg p-4 animate-pulse"
					>
						<div className="flex items-center gap-3">
							<div className="h-6 w-6 bg-carbon-200 dark:bg-carbon-600 rounded-md"></div>
							<div className="flex-1 space-y-2">
								<div className="h-4 bg-carbon-200 dark:bg-carbon-600 rounded w-1/3"></div>
								<div className="h-3 bg-carbon-200 dark:bg-carbon-600 rounded w-1/2"></div>
							</div>
						</div>
					</div>
				))}
			</div>
		);
	}

	if (isError) {
		return (
			<div className="text-center py-16">
				<p className="text-[var(--error)]">Failed to load voice profiles. Please try again.</p>
			</div>
		);
	}

	if (!profiles || profiles.length === 0) {
		return (
			<div className="text-center py-16">
				<div className="bg-[var(--bg-main)] rounded-full w-16 h-16 mx-auto mb-4 flex items-center justify-center border border-[var(--border-subtle)]">
					<Mic2 className="h-8 w-8 text-[var(--text-tertiary)]" />
				</div>
				<h3 className="text-lg font-medium text-[var(--text-primary)] mb-2">No voices yet</h3>
				<p className="text-[var(--text-secondary)] mb-6 max-w-sm mx-auto">
					Create a voice profile to start enrolling speaker samples for recognition.
				</p>
				<Button
					onClick={onCreateProfile}
					variant="outline"
					className="border-[var(--border-subtle)] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-secondary)]"
				>
					Create Voice
				</Button>
			</div>
		);
	}

	return (
		<div className="overflow-x-auto">
			<Table>
				<TableHeader>
					<TableRow className="border-[var(--border-subtle)]">
						<TableHead className="w-10 text-[var(--text-secondary)]" />
						<TableHead className="text-[var(--text-secondary)]">Name</TableHead>
						<TableHead className="text-[var(--text-secondary)]">Samples</TableHead>
						<TableHead className="text-[var(--text-secondary)]">Last Updated</TableHead>
						<TableHead className="text-right text-[var(--text-secondary)]">Actions</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{profiles.map((profile) => {
						const isExpanded = expandedProfileId === profile.id;
						return (
							<Fragment key={profile.id}>
								<TableRow className="border-[var(--border-subtle)] hover:bg-[var(--bg-main)]/30">
									<TableCell>
										<Button
											variant="ghost"
											size="sm"
											onClick={() => toggleExpanded(profile.id)}
											aria-label={isExpanded ? `Collapse samples for ${profile.name}` : `Expand samples for ${profile.name}`}
											aria-expanded={isExpanded}
											className="h-7 w-7 p-0"
										>
											{isExpanded ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
										</Button>
									</TableCell>
									<TableCell>
										<div className="font-medium text-[var(--text-primary)]">{profile.name}</div>
										{profile.notes && (
											<div className="text-xs text-[var(--text-secondary)] mt-0.5 truncate max-w-xs">
												{profile.notes}
											</div>
										)}
									</TableCell>
									<TableCell className="text-[var(--text-secondary)]">{profile.sample_count}</TableCell>
									<TableCell className="text-[var(--text-secondary)]">{formatDateTime(profile.updated_at)}</TableCell>
									<TableCell className="text-right">
										<div className="flex items-center justify-end gap-1">
											<Button
												variant="ghost"
												size="sm"
												onClick={() => onEditProfile(profile)}
												aria-label={`Rename ${profile.name}`}
												className="h-7 w-7 p-0"
											>
												<Pencil className="h-3.5 w-3.5" />
											</Button>
											<Button
												variant="ghost"
												size="sm"
												onClick={() => setProfileToDelete(profile)}
												disabled={deleteProfileMutation.isPending}
												aria-label={`Delete ${profile.name}`}
												className="h-7 w-7 p-0 text-[var(--error)] hover:text-[var(--error)] hover:bg-[var(--error)]/10"
											>
												<Trash2 className="h-3.5 w-3.5" />
											</Button>
										</div>
									</TableCell>
								</TableRow>
								{isExpanded && (
									<TableRow className="border-[var(--border-subtle)] hover:bg-transparent">
										<TableCell colSpan={5} className="bg-[var(--bg-main)]/40">
											<SpeakerProfileSamplesPanel
												profile={profile}
												onMutationError={onMutationError}
												onMutationSuccess={onMutationSuccess}
											/>
										</TableCell>
									</TableRow>
								)}
							</Fragment>
						);
					})}
				</TableBody>
			</Table>

			<AlertDialog
				open={!!profileToDelete}
				onOpenChange={(open) => {
					if (!open) setProfileToDelete(null);
				}}
			>
				<AlertDialogContent className="bg-[var(--bg-card)] border-[var(--border-subtle)]">
					<AlertDialogHeader>
						<AlertDialogTitle className="text-[var(--text-primary)]">Delete voice profile</AlertDialogTitle>
						<AlertDialogDescription className="text-[var(--text-secondary)]">
							{profileToDelete && (
								<>
									Delete "{profileToDelete.name}"? This permanently destroys its {profileToDelete.sample_count}{" "}
									enrolled sample{profileToDelete.sample_count === 1 ? "" : "s"} and cannot be undone.
								</>
							)}
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel className="bg-[var(--bg-secondary)] border-[var(--border-subtle)] text-[var(--text-primary)] hover:bg-[var(--bg-main)]">
							Cancel
						</AlertDialogCancel>
						<AlertDialogAction
							className="bg-[var(--error)] text-white hover:bg-[var(--error)]/90"
							onClick={handleConfirmDeleteProfile}
						>
							Delete Profile
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</div>
	);
}
