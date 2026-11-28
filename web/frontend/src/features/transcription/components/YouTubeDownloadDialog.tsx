import React, { useEffect, useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { languageName } from "@/lib/languageName";
import { Link2, Download, AlertCircle, CheckCircle, AlertTriangle } from "lucide-react";
import { useYouTubeDownload, useYouTubePreview, useTranscriptionProfiles } from "@/features/transcription/hooks/useAudioFiles";

interface YouTubeDownloadDialogProps {
  isOpen: boolean;
  onClose: () => void;
  onDownloadComplete?: () => void;
}

const SUPPORTED_LINK = [
  /^https?:\/\/(www\.|m\.|music\.)?youtube\.com\//i,
  /^https?:\/\/youtu\.be\//i,
  /^https?:\/\/(www\.)?instagram\.com\/(reel|reels|p)\/[\w-]+\/?(\?.*)?$/i,
  /^https?:\/\/(www\.|m\.)?tiktok\.com\/@[^/]+\/video\/\d+\/?(\?.*)?$/i,
  /^https?:\/\/(vm|vt)\.tiktok\.com\/[\w-]+\/?(\?.*)?$/i,
];

function validateLink(value: string): boolean {
  return SUPPORTED_LINK.some((pattern) => pattern.test(value.trim()));
}

function formatDuration(totalSeconds: number): string {
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  const pad = (value: number) => value.toString().padStart(2, "0");
  return hours > 0 ? `${hours}:${pad(minutes)}:${pad(seconds)}` : `${minutes}:${pad(seconds)}`;
}

export function YouTubeDownloadDialog({
  isOpen,
  onClose,
  onDownloadComplete
}: YouTubeDownloadDialogProps) {
  const { mutateAsync: downloadYouTube, isPending: isDownloading } = useYouTubeDownload();
  const [url, setUrl] = useState("");
  const [title, setTitle] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);
  const [notQueued, setNotQueued] = useState<{ title: string; reason: string } | null>(null);
  const [queuedTitle, setQueuedTitle] = useState("");
  const [debouncedUrl, setDebouncedUrl] = useState("");
  const [chosenProfileId, setChosenProfileId] = useState<string | null>(null);
  const { data: profiles } = useTranscriptionProfiles();

  useEffect(() => {
    const handle = setTimeout(() => setDebouncedUrl(url.trim()), 500);
    return () => clearTimeout(handle);
  }, [url]);

  const urlLooksValid = validateLink(debouncedUrl);
  const preview = useYouTubePreview(debouncedUrl, isOpen && urlLooksValid);
  const previewData = urlLooksValid ? preview.data : undefined;
  const previewLoading = urlLooksValid && (url.trim() !== debouncedUrl || preview.isFetching);
  const previewError = urlLooksValid && url.trim() === debouncedUrl && preview.isError
    ? (preview.error instanceof Error ? preview.error.message : "Could not look up the link.")
    : null;
  const isPost = previewData?.kind === "post";
  // A post is never transcribed, but the backend still requires a profile.
  const profileId = chosenProfileId ?? previewData?.suggested_profile_id ?? (isPost ? profiles?.[0]?.id : undefined) ?? "";
  const canSubmit = Boolean(previewData) && !previewLoading && Boolean(profileId) && !isDownloading;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();

    if (!url.trim()) {
      setError("Please enter a link");
      return;
    }

    if (!validateLink(url)) {
      setError("Please enter a YouTube, Instagram or TikTok link");
      return;
    }

    if (!profileId) {
      setError("Choose a transcription profile first");
      return;
    }

    setError(null);

    try {
      const job = await downloadYouTube({ url, title, profileId });
      const jobTitle = job.title || "The recording";
      if (job.status === "pending") {
        setQueuedTitle(jobTitle);
        setSuccess(true);
        setTimeout(() => {
          handleClose();
          onDownloadComplete?.();
        }, 2000);
      } else {
        setNotQueued({
          title: jobTitle,
          reason: job.error_message || "It was downloaded but could not be queued.",
        });
        onDownloadComplete?.();
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Network error occurred. Please try again.");
    }
  };

  const handleClose = () => {
    setTitle("");
    setUrl("");
    setDebouncedUrl("");
    setChosenProfileId(null);
    setError(null);
    setSuccess(false);
    setNotQueued(null);
    setQueuedTitle("");
    onClose();
  };

  return (
    <Dialog open={isOpen} onOpenChange={handleClose}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Link2 className="h-5 w-5 text-[var(--brand-solid)]" />
            Import a link
          </DialogTitle>
          <DialogDescription>
            Paste a YouTube, Instagram or TikTok link to download its audio for transcription
          </DialogDescription>
        </DialogHeader>

        {notQueued ? (
          <div className="space-y-4 py-4">
            <div className="flex items-center justify-center">
              <AlertTriangle className="h-12 w-12 text-[var(--warning)]" />
            </div>
            <div className="text-center">
              <h3 className="font-medium text-[var(--text-primary)] mb-2">
                Downloaded, not queued
              </h3>
              <p className="text-sm text-[var(--text-secondary)]">
                {notQueued.title} was downloaded, but it is not queued for transcription. {notQueued.reason}
              </p>
              <p className="text-sm text-[var(--text-secondary)] mt-2">
                You can start it manually from the job list.
              </p>
            </div>
            <div className="flex justify-end">
              <Button type="button" onClick={handleClose}>
                Close
              </Button>
            </div>
          </div>
        ) : success ? (
          <div className="space-y-4 py-4">
            <div className="flex items-center justify-center">
              <CheckCircle className="h-12 w-12 text-[var(--success)]" />
            </div>
            <div className="text-center">
              <h3 className="font-medium text-[var(--text-primary)] mb-2">
                Queued for transcription
              </h3>
              <p className="text-sm text-[var(--text-secondary)]">
                {queuedTitle} was downloaded and is queued for transcription.
              </p>
            </div>
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="youtube-url">Link</Label>
              <Input
                id="youtube-url"
                type="url"
                placeholder="YouTube, Instagram or TikTok link"
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                disabled={isDownloading}
              />

              {previewLoading && (
                <div className="mt-2 rounded-md border border-[var(--border-subtle)] overflow-hidden" aria-live="polite">
                  <div className="w-full aspect-video bg-gray-200 dark:bg-gray-700 motion-safe:animate-pulse" />
                  <div className="p-3 space-y-2">
                    <div className="h-4 w-3/4 rounded bg-gray-200 dark:bg-gray-700 motion-safe:animate-pulse" />
                    <div className="h-3.5 w-1/2 rounded bg-gray-200 dark:bg-gray-700 motion-safe:animate-pulse" />
                    <div className="h-3.5 w-1/3 rounded bg-gray-200 dark:bg-gray-700 motion-safe:animate-pulse" />
                    <p className="text-sm text-[var(--text-secondary)]">Looking up the link...</p>
                  </div>
                </div>
              )}

              {!previewLoading && previewError && (
                <div className="flex items-center gap-2 p-3 bg-[var(--error)]/10 border border-[var(--error)]/20 rounded-[var(--radius-input)]">
                  <AlertCircle className="h-4 w-4 shrink-0 text-[var(--error)]" />
                  <p className="text-sm text-[var(--error)]">{previewError}</p>
                </div>
              )}

              {!previewLoading && previewData && (
                <div className="mt-2 rounded-md border border-[var(--border-subtle)] overflow-hidden">
                  <img
                    src={previewData.thumbnail_url}
                    alt=""
                    className="w-full aspect-video object-cover"
                    onError={(e) => {
                      (e.target as HTMLImageElement).style.display = "none";
                    }}
                  />
                  <div className="p-3 space-y-1">
                    <p className="font-medium text-[var(--text-primary)] leading-snug">{previewData.title}</p>
                    {isPost ? (
                      <>
                        <p className="text-sm text-[var(--text-secondary)]">{previewData.channel}</p>
                        <p className="text-sm text-[var(--text-secondary)]">
                          {`Post · ${previewData.image_count ?? 0} ${previewData.image_count === 1 ? "image" : "images"}`}
                        </p>
                      </>
                    ) : (
                      <>
                        <p className="text-sm text-[var(--text-secondary)]">
                          {[
                            previewData.channel,
                            typeof previewData.duration_seconds === "number" ? formatDuration(previewData.duration_seconds) : null,
                          ].filter(Boolean).join(" · ")}
                        </p>
                        <p className="text-sm text-[var(--text-secondary)]">
                          {previewData.language
                            ? `${languageName(previewData.language)} (${previewData.language})`
                            : "Language not reported"}
                        </p>
                      </>
                    )}
                  </div>
                </div>
              )}
            </div>

            {previewData && !previewLoading && !isPost && (
              <div className="space-y-2">
                <Label htmlFor="youtube-profile">Transcription profile</Label>
                <Select value={profileId} onValueChange={setChosenProfileId}>
                  <SelectTrigger id="youtube-profile" className="w-full">
                    <SelectValue placeholder="Choose a profile" />
                  </SelectTrigger>
                  <SelectContent>
                    {(profiles ?? []).map((profile) => (
                      <SelectItem key={profile.id} value={profile.id}>
                        {profile.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                {!previewData.suggested_profile_id && !chosenProfileId && (
                  <p className="text-xs text-[var(--text-tertiary)]">
                    {previewData.language
                      ? "No profile matches the reported language. Choose one."
                      : "No language was reported. Choose a profile."}
                  </p>
                )}
              </div>
            )}

            <div className="space-y-2">
              <Label htmlFor="custom-title">Custom Title (Optional)</Label>
              <Input
                id="custom-title"
                type="text"
                placeholder="Leave empty to use the post title"
                value={title}
                onChange={(e) => setTitle(e.target.value)}
                disabled={isDownloading}
              />
              <p className="text-xs text-[var(--text-tertiary)]">
                If left empty, the post's title will be used automatically
              </p>
            </div>

            {error && (
              <div className="flex items-center gap-2 p-3 bg-[var(--error)]/10 border border-[var(--error)]/20 rounded-[var(--radius-input)]">
                <AlertCircle className="h-4 w-4 text-[var(--error)]" />
                <p className="text-sm text-[var(--error)]">{error}</p>
              </div>
            )}

            <div className="flex gap-2 justify-end pt-4">
              <Button
                type="button"
                variant="outline"
                onClick={handleClose}
                disabled={isDownloading}
              >
                Cancel
              </Button>
              <Button
                type="submit"
                disabled={!canSubmit}
                className="min-w-24"
              >
                {isDownloading ? (
                  <>
                    <div className="w-4 h-4 border-2 border-white border-t-transparent rounded-full animate-spin mr-2" />
                    Downloading and queueing...
                  </>
                ) : (
                  <>
                    <Download className="h-4 w-4 mr-2" />
                    Download and queue
                  </>
                )}
              </Button>
            </div>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}