import { useCallback, useEffect, useState } from "react";
import { Mic2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { SpeakerProfilesTable } from "./SpeakerProfilesTable";
import {
	useCreateSpeakerProfile,
	useUpdateSpeakerProfile,
	type SpeakerProfile,
} from "../hooks/useSpeakerProfiles";

export function SpeakerProfileSettings() {
	const [dialogOpen, setDialogOpen] = useState(false);
	const [editingProfile, setEditingProfile] = useState<SpeakerProfile | null>(null);
	const [name, setName] = useState("");
	const [notes, setNotes] = useState("");
	const [dialogError, setDialogError] = useState("");
	const [bannerError, setBannerError] = useState("");
	const [bannerSuccess, setBannerSuccess] = useState("");

	const createProfileMutation = useCreateSpeakerProfile();
	const updateProfileMutation = useUpdateSpeakerProfile();
	const isSaving = createProfileMutation.isPending || updateProfileMutation.isPending;

	// Clear a stale banner once its message has been visible for a moment,
	// so a later silent state can't be misread as still confirmed.
	useEffect(() => {
		if (!bannerSuccess) return;
		const timeout = setTimeout(() => setBannerSuccess(""), 6000);
		return () => clearTimeout(timeout);
	}, [bannerSuccess]);

	const handleCreateProfile = useCallback(() => {
		setEditingProfile(null);
		setName("");
		setNotes("");
		setDialogError("");
		setDialogOpen(true);
	}, []);

	const handleEditProfile = useCallback((profile: SpeakerProfile) => {
		setEditingProfile(profile);
		setName(profile.name);
		setNotes(profile.notes);
		setDialogError("");
		setDialogOpen(true);
	}, []);

	const handleDialogOpenChange = useCallback((open: boolean) => {
		if (isSaving) return;
		setDialogOpen(open);
		if (!open) {
			setEditingProfile(null);
			setDialogError("");
		}
	}, [isSaving]);

	const handleSubmit = useCallback(
		async (event: React.FormEvent) => {
			event.preventDefault();
			const trimmedName = name.trim();
			if (!trimmedName) {
				setDialogError("Name is required.");
				return;
			}

			try {
				setDialogError("");
				if (editingProfile) {
					await updateProfileMutation.mutateAsync({
						id: editingProfile.id,
						name: trimmedName,
						notes: notes.trim(),
					});
					setBannerSuccess(`Renamed voice to "${trimmedName}".`);
				} else {
					await createProfileMutation.mutateAsync({
						name: trimmedName,
						notes: notes.trim(),
					});
					setBannerSuccess(`Created voice "${trimmedName}".`);
				}
				setBannerError("");
				setDialogOpen(false);
				setEditingProfile(null);
			} catch (error) {
				setDialogError(error instanceof Error ? error.message : "Failed to save voice profile.");
			}
		},
		[name, notes, editingProfile, createProfileMutation, updateProfileMutation],
	);

	const handleMutationError = useCallback((message: string) => {
		setBannerSuccess("");
		setBannerError(message);
	}, []);

	const handleMutationSuccess = useCallback((message: string) => {
		setBannerError("");
		setBannerSuccess(message);
	}, []);

	return (
		<div className="space-y-6">
			{bannerError && (
				<div className="bg-[var(--error)]/10 border border-[var(--error)]/20 rounded-lg p-3">
					<p className="text-[var(--error)] text-sm">{bannerError}</p>
				</div>
			)}

			{bannerSuccess && (
				<div className="bg-[var(--success-translucent)] border border-[var(--success-solid)]/20 rounded-lg p-3">
					<p className="text-[var(--success-solid)] text-sm">{bannerSuccess}</p>
				</div>
			)}

			<div className="bg-[var(--bg-main)]/50 border border-[var(--border-subtle)] rounded-[var(--radius-card)] p-4 sm:p-6 shadow-sm">
				<div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3 sm:gap-0 mb-4">
					<div className="flex items-center gap-2">
						<Mic2 className="h-5 w-5 text-[var(--brand-solid)]" />
						<div>
							<h3 className="text-lg font-medium text-[var(--text-primary)]">Enrolled Voices</h3>
							<p className="text-sm text-[var(--text-secondary)] mt-1">
								Manage the voice profiles used to recognize speakers across your transcripts.
							</p>
						</div>
					</div>
					<Button
						onClick={handleCreateProfile}
						className="!bg-[var(--brand-gradient)] hover:!opacity-90 !text-black dark:!text-white shadow-lg shadow-orange-500/20 border-none"
					>
						Create Voice
					</Button>
				</div>

				<SpeakerProfilesTable
					onEditProfile={handleEditProfile}
					onCreateProfile={handleCreateProfile}
					onMutationError={handleMutationError}
					onMutationSuccess={handleMutationSuccess}
				/>
			</div>

			<Dialog open={dialogOpen} onOpenChange={handleDialogOpenChange}>
				<DialogContent className="sm:max-w-md bg-[var(--bg-card)] border-[var(--border-subtle)] text-[var(--text-primary)]">
					<DialogHeader>
						<DialogTitle>{editingProfile ? "Rename Voice" : "Create Voice"}</DialogTitle>
						<DialogDescription>
							{editingProfile
								? "Update the name or notes for this voice profile."
								: "Name a new voice profile. You can enroll samples against it afterward."}
						</DialogDescription>
					</DialogHeader>

					<form onSubmit={handleSubmit} className="space-y-4">
						<div className="space-y-2">
							<Label htmlFor="speaker-profile-name">Name *</Label>
							<Input
								id="speaker-profile-name"
								placeholder="e.g., Alex"
								value={name}
								onChange={(event) => setName(event.target.value)}
								maxLength={255}
								disabled={isSaving}
							/>
						</div>

						<div className="space-y-2">
							<Label htmlFor="speaker-profile-notes">Notes</Label>
							<Textarea
								id="speaker-profile-notes"
								placeholder="Optional notes to help identify this voice"
								value={notes}
								onChange={(event) => setNotes(event.target.value)}
								rows={3}
								disabled={isSaving}
							/>
						</div>

						{dialogError && (
							<div className="text-sm text-[var(--error)] bg-[var(--error)]/10 p-3 rounded-lg">
								{dialogError}
							</div>
						)}

						<DialogFooter>
							<Button
								type="button"
								variant="outline"
								onClick={() => handleDialogOpenChange(false)}
								disabled={isSaving}
							>
								Cancel
							</Button>
							<Button
								type="submit"
								disabled={isSaving || !name.trim()}
								className="!bg-[var(--brand-gradient)] hover:!opacity-90 !text-black dark:!text-white border-none"
							>
								{isSaving ? "Saving..." : editingProfile ? "Save Changes" : "Create Voice"}
							</Button>
						</DialogFooter>
					</form>
				</DialogContent>
			</Dialog>
		</div>
	);
}
