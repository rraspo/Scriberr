package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"scriberr/internal/models"
	"scriberr/pkg/logger"

	"github.com/google/uuid"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	linkKindPost           = "post"
	linkKindVideo          = "video"
	noVideoFormatsMarker   = "no video formats found"
	maxPostImageBytes      = 30 << 20
	postImageTimeout       = 60 * time.Second
	postImageJPEGQuality   = 90
	postThumbnailWidth     = 320
	defaultPostTitle       = "Instagram post"
	instagramMediaVideo    = 2
	postImageDownloadError = "The post's images could not be downloaded. Try again later."
)

var (
	postShortcodePattern = regexp.MustCompile(`^/(?:reel|reels|p)/([A-Za-z0-9_-]+)`)
	jsonScriptPattern    = regexp.MustCompile(`(?s)<script type="application/json"[^>]*>(.*?)</script>`)
)

// postImage is one item of an Instagram post: the image to keep and its label.
type postImage struct {
	url   string
	label string
}

// instagramPost is what the logged-out post page tells about a post.
type instagramPost struct {
	caption  string
	username string
	images   []postImage
}

// isNoVideoFormats reports yt-dlp's complaint about a post that has no video.
func isNoVideoFormats(stderr string) bool {
	return strings.Contains(strings.ToLower(stderr), noVideoFormatsMarker)
}

// isInstagramImageHost accepts only Instagram's own image CDN over https:
// the URLs come from a page fetched off the internet.
func isInstagramImageHost(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	for _, domain := range []string{"cdninstagram.com", "fbcdn.net"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

// newPostImageClient builds the client that downloads a post's images; a nil
// transport means a real one that goes through the link-import proxy.
func newPostImageClient(proxy string, transport http.RoundTripper) *http.Client {
	if transport == nil {
		realTransport := &http.Transport{}
		if proxy != "" {
			if proxyURL, err := url.Parse(proxy); err == nil {
				realTransport.Proxy = http.ProxyURL(proxyURL)
			}
		}
		transport = realTransport
	}
	return &http.Client{
		Transport: transport,
		Timeout:   postImageTimeout,
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			if !isInstagramImageHost(request.URL.String()) {
				return fmt.Errorf("redirect outside Instagram's image hosts")
			}
			return nil
		},
	}
}

// fetchInstagramPost reads a post's media from the page yt-dlp fetched. yt-dlp
// fails on image posts by design, so its exit status is ignored and only the
// page dumps it leaves in its working directory matter. A page with no media
// is Instagram's login wall.
func (h *Handler) fetchInstagramPost(ctx context.Context, link, jobID string) (*instagramPost, *linkFailure) {
	parsed, err := url.Parse(link)
	if err != nil {
		return nil, &linkFailure{http.StatusBadRequest, "invalid_url", unsupportedLinkMessage}
	}
	match := postShortcodePattern.FindStringSubmatch(parsed.Path)
	if match == nil {
		return nil, &linkFailure{http.StatusBadRequest, "invalid_url", unsupportedLinkMessage}
	}
	shortcode := match[1]

	pagesDir := filepath.Join(h.config.UploadDir, "post-pages-"+jobID)
	if err := os.MkdirAll(pagesDir, 0o755); err != nil {
		return nil, &linkFailure{http.StatusInternalServerError, "download_failed", "Failed to create upload directory"}
	}
	defer os.RemoveAll(pagesDir)

	command := exec.CommandContext(ctx, "yt-dlp", withLinkProxy(h.config.LinkImportProxy, sourceInstagram,
		"--write-pages", "--skip-download", "--ignore-errors", "--no-playlist", link)...)
	command.Dir = pagesDir
	var stderr bytes.Buffer
	command.Stderr = &stderr
	_ = command.Run()

	failure := classifyLinkFailure(sourceInstagram, stderr.String())
	if failure.code == "rate_limited" || failure.code == "instagram_login_required" {
		return nil, &failure
	}

	dumps, _ := filepath.Glob(filepath.Join(pagesDir, "*.dump"))
	var media map[string]any
	for _, dump := range dumps {
		content, readErr := os.ReadFile(dump)
		if readErr != nil {
			continue
		}
		for _, blob := range jsonScriptPattern.FindAllSubmatch(content, -1) {
			var decoded any
			if json.Unmarshal(blob[1], &decoded) != nil {
				continue
			}
			media = preferMedia(media, findPostMedia(decoded, shortcode))
		}
	}
	if media == nil {
		logger.Warn("Instagram post page carried no media", "job_id", jobID, "stderr", stderr.String())
		return nil, &linkFailure{http.StatusBadGateway, "instagram_login_required", "Instagram asked for a login before showing this post. Try again later."}
	}

	items := carouselItems(media)
	if len(items) == 0 {
		items = []map[string]any{media}
	}
	post := &instagramPost{
		caption:  strings.TrimSpace(nestedString(media, "caption", "text")),
		username: strings.TrimSpace(nestedString(media, "user", "username")),
	}
	for position, item := range items {
		label := fmt.Sprintf("%d/%d", position+1, len(items))
		if isVideoItem(item) {
			label += " video"
		}
		post.images = append(post.images, postImage{url: itemImageURL(item), label: label})
	}
	return post, nil
}

// findPostMedia walks every decoded blob for the object that describes the
// post: the shortcode matches and it carries images or carousel items.
func findPostMedia(node any, shortcode string) map[string]any {
	var best map[string]any
	switch value := node.(type) {
	case map[string]any:
		if code, _ := value["code"].(string); code == shortcode && (value["image_versions2"] != nil || value["carousel_media"] != nil) {
			best = value
		}
		for _, child := range value {
			best = preferMedia(best, findPostMedia(child, shortcode))
		}
	case []any:
		for _, child := range value {
			best = preferMedia(best, findPostMedia(child, shortcode))
		}
	}
	return best
}

// preferMedia keeps the candidate that lists carousel items over one that
// does not, since the same post can appear in a thinner form elsewhere.
func preferMedia(current, candidate map[string]any) map[string]any {
	if current == nil {
		return candidate
	}
	if candidate != nil && len(carouselItems(current)) == 0 && len(carouselItems(candidate)) > 0 {
		return candidate
	}
	return current
}

func carouselItems(media map[string]any) []map[string]any {
	children, _ := media["carousel_media"].([]any)
	items := make([]map[string]any, 0, len(children))
	for _, child := range children {
		if item, ok := child.(map[string]any); ok {
			items = append(items, item)
		}
	}
	return items
}

func isVideoItem(item map[string]any) bool {
	if mediaType, _ := item["media_type"].(float64); int(mediaType) == instagramMediaVideo {
		return true
	}
	versions, _ := item["video_versions"].([]any)
	return len(versions) > 0
}

// itemImageURL is the largest image of an item, else its display URI.
func itemImageURL(item map[string]any) string {
	if versions, ok := item["image_versions2"].(map[string]any); ok {
		if candidates, _ := versions["candidates"].([]any); len(candidates) > 0 {
			if first, ok := candidates[0].(map[string]any); ok {
				if candidateURL, _ := first["url"].(string); candidateURL != "" {
					return candidateURL
				}
			}
		}
	}
	displayURI, _ := item["display_uri"].(string)
	return displayURI
}

func nestedString(node map[string]any, keys ...string) string {
	var current any = node
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[key]
	}
	text, _ := current.(string)
	return text
}

// storePostImages downloads a post's images as the job's frames and tiles them
// into contact sheets. Every URL is checked before the first fetch, and a
// failure leaves nothing behind.
func (h *Handler) storePostImages(ctx context.Context, jobID string, post *instagramPost) (frames []models.JobFrame, sheets []models.JobSheet, failure *linkFailure) {
	for _, item := range post.images {
		if !isInstagramImageHost(item.url) {
			logger.Warn("Instagram post image outside the image hosts", "job_id", jobID)
			return nil, nil, &linkFailure{http.StatusBadGateway, "download_failed", postImageDownloadError}
		}
	}

	root := visualsDir(h.config.UploadDir, jobID)
	_ = os.RemoveAll(root)
	defer func() {
		if failure != nil {
			_ = os.RemoveAll(root)
		}
	}()
	frameDir := filepath.Join(root, "frames")
	if err := os.MkdirAll(frameDir, 0o755); err != nil {
		return nil, nil, &linkFailure{http.StatusInternalServerError, "download_failed", "Could not create the frames folder."}
	}

	client := newPostImageClient(h.config.LinkImportProxy, h.postImageTransport)
	for index, item := range post.images {
		if err := downloadPostImage(ctx, client, item.url, filepath.Join(frameDir, fmt.Sprintf("%d.jpg", index))); err != nil {
			logger.Warn("Instagram post image download failed", "job_id", jobID, "error", err.Error())
			return nil, nil, &linkFailure{http.StatusBadGateway, "download_failed", postImageDownloadError}
		}
		frames = append(frames, models.JobFrame{
			Index: index,
			URL:   fmt.Sprintf("/api/v1/transcription/%s/frames/%d", jobID, index),
			Label: item.label,
		})
	}

	sheets, err := buildContactSheets(frameDir, filepath.Join(root, "sheets"), frames, jobID)
	if err != nil {
		logger.Warn("Contact sheet build failed", "job_id", jobID, "error", err.Error())
		return nil, nil, &linkFailure{http.StatusInternalServerError, "download_failed", "Could not build the contact sheets."}
	}
	return frames, sheets, nil
}

// downloadPostImage saves one image as a JPEG at full resolution: JPEG
// sources keep their bytes, other formats are re-encoded.
func downloadPostImage(ctx context.Context, client *http.Client, imageURL, target string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("image request answered %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxPostImageBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxPostImageBytes {
		return fmt.Errorf("image is over %d bytes", maxPostImageBytes)
	}
	decoded, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return err
	}
	if format == "jpeg" {
		return os.WriteFile(target, data, 0o644)
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, decoded, &jpeg.Options{Quality: postImageJPEGQuality}); err != nil {
		return err
	}
	return os.WriteFile(target, encoded.Bytes(), 0o644)
}

// savePostThumbnail scales a post's first image to the list thumbnail width.
func savePostThumbnail(uploadDir, jobID, firstFramePath string) *string {
	source, err := decodeJPEGFile(firstFramePath)
	if err != nil {
		logger.Warn("Cannot read the post's first image", "job_id", jobID, "error", err.Error())
		return nil
	}
	bounds := source.Bounds()
	height := int(math.Round(float64(postThumbnailWidth) * float64(bounds.Dy()) / float64(bounds.Dx())))
	if height < 1 {
		height = 1
	}
	scaled := image.NewRGBA(image.Rect(0, 0, postThumbnailWidth, height))
	xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), source, bounds, xdraw.Src, nil)

	thumbnailDir := filepath.Join(uploadDir, "thumbnails")
	if err := os.MkdirAll(thumbnailDir, 0o755); err != nil {
		logger.Warn("Cannot create thumbnail directory", "error", err.Error())
		return nil
	}
	thumbnailPath := filepath.Join(thumbnailDir, jobID+".jpg")
	if err := writeJPEGFile(thumbnailPath, scaled); err != nil {
		_ = os.Remove(thumbnailPath)
		logger.Warn("Cannot save the post thumbnail", "job_id", jobID, "error", err.Error())
		return nil
	}
	return &thumbnailPath
}

// importInstagramPost stores an image post as a completed job: no audio, no
// transcription, its images as the job's frames.
func (h *Handler) importInstagramPost(ctx context.Context, request linkImportRequest, jobID string) (*models.TranscriptionJob, *linkFailure) {
	post, failure := h.fetchInstagramPost(ctx, request.URL, jobID)
	if failure != nil {
		return nil, failure
	}
	frames, sheets, failure := h.storePostImages(ctx, jobID, post)
	if failure != nil {
		return nil, failure
	}

	title := defaultPostTitle
	if line := captionTitle(post.caption); line != "" {
		title = line
	}
	if request.Title != nil && *request.Title != "" {
		title = *request.Title
	}
	sourceURL := request.URL
	linkKind := linkKindPost
	job := models.TranscriptionJob{
		ID:            jobID,
		Status:        models.StatusCompleted,
		Source:        sourceInstagram,
		SourceURL:     &sourceURL,
		Title:         &title,
		SourceChannel: authorHandle(post.username),
		LinkKind:      &linkKind,
		Frames:        frames,
		Sheets:        sheets,
		VisualsStatus: models.VisualsReady,
	}
	if post.caption != "" {
		caption := post.caption
		job.SourceDescription = &caption
	}
	if request.ExternalID != "" {
		externalID := request.ExternalID
		job.ExternalID = &externalID
	}
	job.ThumbnailPath = savePostThumbnail(h.config.UploadDir, jobID, filepath.Join(visualsDir(h.config.UploadDir, jobID), "frames", "0.jpg"))

	if err := h.jobRepo.Create(ctx, &job); err != nil {
		_ = os.RemoveAll(visualsDir(h.config.UploadDir, jobID))
		if job.ThumbnailPath != nil {
			_ = os.Remove(*job.ThumbnailPath)
		}
		return nil, &linkFailure{http.StatusInternalServerError, "download_failed", "Failed to save transcription record"}
	}
	applySourceDescription(&job)
	return &job, nil
}

// previewInstagramPost answers the dialog's preview for an image post.
func (h *Handler) previewInstagramPost(ctx context.Context, link string) (map[string]any, *linkFailure) {
	post, failure := h.fetchInstagramPost(ctx, link, uuid.New().String())
	if failure != nil {
		return nil, failure
	}
	title := defaultPostTitle
	if line := captionTitle(post.caption); line != "" {
		title = line
	}
	return map[string]any{
		"video_id":             "",
		"title":                title,
		"channel":              authorHandle(post.username),
		"duration_seconds":     nil,
		"language":             nil,
		"thumbnail_url":        "",
		"kind":                 linkKindPost,
		"image_count":          len(post.images),
		"suggested_profile_id": nil,
	}, nil
}

// rebuildPostVisuals fetches a post's page and images again. The job stays
// completed; only its visuals change.
func (h *Handler) rebuildPostVisuals(ctx context.Context, job *models.TranscriptionJob) {
	post, failure := h.fetchInstagramPost(ctx, *job.SourceURL, job.ID)
	var frames []models.JobFrame
	var sheets []models.JobSheet
	if failure == nil {
		frames, sheets, failure = h.storePostImages(ctx, job.ID, post)
	}

	fresh, err := h.jobRepo.FindByID(ctx, job.ID)
	if err != nil {
		// The job was deleted while its images were fetched.
		_ = os.RemoveAll(visualsDir(h.config.UploadDir, job.ID))
		return
	}
	if failure != nil {
		reason := "The post's images could not be fetched. " + failure.message
		fresh.VisualsStatus = models.VisualsFailed
		fresh.VisualsError = &reason
		fresh.Frames = nil
		fresh.Sheets = nil
	} else {
		fresh.VisualsStatus = models.VisualsReady
		fresh.VisualsError = nil
		fresh.Frames = frames
		fresh.Sheets = sheets
	}
	if err := h.saveVisuals(ctx, fresh); err != nil {
		logger.Warn("Could not save the post visuals", "job_id", job.ID, "error", err.Error())
	}
}
