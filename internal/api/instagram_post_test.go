package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"scriberr/internal/models"
	"scriberr/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// fakePostYtDlpScript stands in for yt-dlp on an Instagram post that has no
// video. Metadata and downloads fail the way yt-dlp fails on image posts
// ("No video formats found"); --write-pages copies FAKE_POST_PAGE into the
// working directory as the post page's dump, which is where yt-dlp writes it.
// FAKE_POST_STDERR replaces the error yt-dlp prints.
const fakePostYtDlpScript = `#!/bin/sh
if [ -n "$FAKE_YTDLP_LOG" ]; then echo "$*" >> "$FAKE_YTDLP_LOG"; fi
error="${FAKE_POST_STDERR:-ERROR: [Instagram] Dfds-b_xymf: No video formats found!; please report this issue on  https://github.com/yt-dlp/yt-dlp/issues?q= , filling out the appropriate issue template. Confirm you are on the latest version using  yt-dlp -U}"
for argument in "$@"; do
  if [ "$argument" = "--write-pages" ]; then
    if [ -n "$FAKE_POST_PAGE" ]; then
      cp "$FAKE_POST_PAGE" "./DfdtAkRG_g9_https_-_www.instagram.com_p_DfdtAkRG_g9.dump"
    fi
    printf '<html>home</html>' > "./https_-_www.instagram.com_.dump"
  fi
done
echo "$error" >&2
exit 1
`

const postShortcode = "DfdtAkRG_g9"
const postLink = "https://www.instagram.com/p/" + postShortcode + "/"
const postCaption = "Comment “AI” to get the link.\n\nNVIDIA JUST OPEN-SOURCED A SEMANTIC LAYER\n#ai #agents"

// postItem is one image (or video) of a post as Instagram embeds it in the
// logged-out post page.
type postItem struct {
	code     string
	imageURL string
	video    bool
}

// postPageHTML builds a post page the way Instagram serves it to a logged-out
// visitor: the media sits in one of many application/json script blobs, under
// require/__bbox/result/data/xig_polaris_media/if_not_gated_logged_out.
// With no items the page carries no media (a login wall).
func postPageHTML(t *testing.T, items []postItem, carousel bool) string {
	t.Helper()
	itemJSON := func(item postItem) map[string]any {
		mediaType := 1
		typename := "XIGPolarisImageMedia"
		entry := map[string]any{
			"code":            item.code,
			"display_uri":     item.imageURL + "&small=1",
			"original_width":  1080,
			"original_height": 1350,
			"image_versions2": map[string]any{"candidates": []map[string]any{
				{"url": item.imageURL},
				{"url": item.imageURL + "&w=150"},
			}},
		}
		if item.video {
			mediaType = 2
			typename = "XIGPolarisVideoMedia"
			entry["video_versions"] = []map[string]any{{"url": "https://scontent.cdninstagram.com/v/clip.mp4?x=1"}}
		}
		entry["__typename"] = typename
		entry["media_type"] = mediaType
		return entry
	}
	blobs := []string{`{"require":[["ScheduledServerJS","handle",null,[{"__bbox":{"define":[["Noise",[],{"code":"OTHERPOST1"},1]]}}]]]}`}
	if len(items) > 0 {
		var media map[string]any
		if carousel {
			children := make([]map[string]any, 0, len(items))
			for _, item := range items {
				children = append(children, itemJSON(item))
			}
			media = map[string]any{
				"__typename":      "XIGPolarisCarouselMedia",
				"code":            postShortcode,
				"media_type":      8,
				"product_type":    "carousel_container",
				"carousel_media":  children,
				"image_versions2": map[string]any{"candidates": []map[string]any{{"url": items[0].imageURL}}},
			}
		} else {
			media = itemJSON(items[0])
			media["code"] = postShortcode
			media["carousel_media"] = nil
		}
		media["caption"] = map[string]any{"text": postCaption}
		media["user"] = map[string]any{"pk": "73453916575", "username": "simplifyinai"}
		media["taken_at"] = 1794000000
		wrapped := map[string]any{"require": []any{[]any{"ScheduledServerJS", "handle", nil, []any{map[string]any{
			"__bbox": map[string]any{"result": map[string]any{"data": map[string]any{"xig_polaris_media": map[string]any{"if_not_gated_logged_out": media}}}},
		}}}}}
		encoded, err := json.Marshal(wrapped)
		require.NoError(t, err)
		blobs = append(blobs, string(encoded))
	}
	var page strings.Builder
	page.WriteString("<!DOCTYPE html><html><head><title>Instagram</title></head><body>")
	for _, blob := range blobs {
		page.WriteString(`<script type="application/json" data-content-len="1" data-sjs>`)
		page.WriteString(blob)
		page.WriteString("</script>")
	}
	page.WriteString("</body></html>")
	return page.String()
}

// fakeInstagramCDN serves a real JPEG for every request and records the URLs
// asked for, in order.
type fakeInstagramCDN struct {
	mutex     sync.Mutex
	requested []string
	jpegBytes []byte
}

func (cdn *fakeInstagramCDN) RoundTrip(request *http.Request) (*http.Response, error) {
	cdn.mutex.Lock()
	cdn.requested = append(cdn.requested, request.URL.String())
	cdn.mutex.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"image/jpeg"}},
		Body:       io.NopCloser(bytes.NewReader(cdn.jpegBytes)),
		Request:    request,
	}, nil
}

func (cdn *fakeInstagramCDN) urls() []string {
	cdn.mutex.Lock()
	defer cdn.mutex.Unlock()
	return append([]string(nil), cdn.requested...)
}

// installInstagramPost wires a fake yt-dlp that serves the given post page and
// a fake CDN for its images. It returns the yt-dlp call log and the CDN.
func installInstagramPost(t *testing.T, handler *Handler, items []postItem, carousel bool) (string, *fakeInstagramCDN) {
	t.Helper()
	binDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "yt-dlp"), []byte(fakePostYtDlpScript), 0o755))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logPath := filepath.Join(binDir, "calls.log")
	t.Setenv("FAKE_YTDLP_LOG", logPath)
	pagePath := filepath.Join(binDir, "post.html")
	require.NoError(t, os.WriteFile(pagePath, []byte(postPageHTML(t, items, carousel)), 0o644))
	t.Setenv("FAKE_POST_PAGE", pagePath)
	t.Setenv("FAKE_POST_STDERR", "")

	fixture := filepath.Join(binDir, "image.jpg")
	writeTestJPEG(t, fixture)
	jpegBytes, err := os.ReadFile(fixture)
	require.NoError(t, err)
	cdn := &fakeInstagramCDN{jpegBytes: jpegBytes}
	handler.postImageTransport = cdn
	return logPath, cdn
}

func threeImageCarousel() []postItem {
	return []postItem{
		{code: "Dfds-b_xymf", imageURL: "https://instagram.fgdl17-1.fna.fbcdn.net/v/t51/one.jpg?stp=1"},
		{code: "Dfds_y-RnPM", imageURL: "https://scontent.cdninstagram.com/v/t51/two.jpg?stp=2"},
		{code: "Dfds_2GxJOq", imageURL: "https://instagram.fgdl17-1.fna.fbcdn.net/v/t51/three.jpg?stp=3"},
	}
}

type postImportBody struct {
	JobID         string  `json:"job_id"`
	Status        string  `json:"status"`
	Title         string  `json:"title"`
	Source        string  `json:"source"`
	Kind          string  `json:"kind"`
	ImageCount    int     `json:"image_count"`
	ProfileName   *string `json:"profile_name"`
	Error         *string `json:"error"`
	VisualsStatus string  `json:"visuals_status"`
	VisualsURL    string  `json:"visuals_url"`
}

func decodePostImport(t *testing.T, recorder *httptest.ResponseRecorder) postImportBody {
	t.Helper()
	var body postImportBody
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body), recorder.Body.String())
	return body
}

// An Instagram post made only of images is stored as a completed "post" job:
// no audio and no transcription, its images in carousel order as the job's
// frames, with contact sheets, its caption and the author's @handle.
func TestAnInstagramImageCarouselIsStoredAsAPost(t *testing.T) {
	handler, db, uploadDir := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)
	handler.config.LinkImportProxy = "http://egress.example:8888"
	ytDlpLog, cdn := installInstagramPost(t, handler, threeImageCarousel(), true)

	response := performLinkImport(t, handler, map[string]any{"url": postLink, "external_id": "wa-msg-1"})
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	created := decodePostImport(t, response)
	require.Equal(t, "completed", created.Status)
	require.Equal(t, "post", created.Kind)
	require.Equal(t, 3, created.ImageCount)
	require.Equal(t, "instagram", created.Source)
	require.Equal(t, "Comment “AI” to get the link.", created.Title)
	require.Nil(t, created.Error)
	require.Nil(t, created.ProfileName, "nothing is transcribed, so no profile is used")
	require.Equal(t, "ready", created.VisualsStatus)
	require.Equal(t, "/api/v1/transcription/"+created.JobID+"/visuals", created.VisualsURL)

	require.Equal(t, []string{
		"https://instagram.fgdl17-1.fna.fbcdn.net/v/t51/one.jpg?stp=1",
		"https://scontent.cdninstagram.com/v/t51/two.jpg?stp=2",
		"https://instagram.fgdl17-1.fna.fbcdn.net/v/t51/three.jpg?stp=3",
	}, cdn.urls(), "the largest image of each item, in carousel order")

	calls := readCalls(t, ytDlpLog)
	for _, call := range calls {
		require.Contains(t, call, "--proxy http://egress.example:8888", "every Instagram call goes through the proxy")
		require.NotContains(t, call, "--extract-audio", "an image post is never downloaded as video")
	}
	require.Contains(t, calls[len(calls)-1], "--write-pages")

	var stored models.TranscriptionJob
	require.NoError(t, db.First(&stored, "id = ?", created.JobID).Error)
	require.Equal(t, models.StatusCompleted, stored.Status)
	require.Equal(t, "", stored.AudioPath)
	require.Nil(t, stored.Transcript)
	require.NotNil(t, stored.LinkKind)
	require.Equal(t, "post", *stored.LinkKind)
	require.Equal(t, "@simplifyinai", *stored.SourceChannel)
	require.Equal(t, postCaption, *stored.SourceDescription)
	require.Equal(t, postLink, *stored.SourceURL)
	require.Equal(t, "wa-msg-1", *stored.ExternalID)
	require.Nil(t, stored.VideoPath)
	require.NotNil(t, stored.ThumbnailPath, "the first image is the list thumbnail")
	require.FileExists(t, *stored.ThumbnailPath)

	visuals := decodeVisuals(t, getVisuals(handler, created.JobID))
	require.Equal(t, "ready", visuals.Status)
	require.Len(t, visuals.Frames, 3)
	for index, frame := range visuals.Frames {
		require.Equal(t, index, frame.Index)
		require.Equal(t, fmt.Sprintf("%d/3", index+1), frame.Label)
		require.Equal(t, fmt.Sprintf("/api/v1/transcription/%s/frames/%d", created.JobID, index), frame.URL)
		framePath := filepath.Join(uploadDir, "visuals", created.JobID, "frames", fmt.Sprintf("%d.jpg", index))
		file, err := os.Open(framePath)
		require.NoError(t, err)
		_, decodeErr := jpeg.DecodeConfig(file)
		file.Close()
		require.NoError(t, decodeErr, "each frame is a JPEG")
	}
	require.Len(t, visuals.Sheets, 1)
	require.Nil(t, visuals.Video)
	require.FileExists(t, filepath.Join(uploadDir, "visuals", created.JobID, "sheets", "0.jpg"))

	entries, err := os.ReadDir(uploadDir)
	require.NoError(t, err)
	for _, entry := range entries {
		require.False(t, strings.Contains(entry.Name(), created.JobID) && !entry.IsDir(), "no audio or page files are left in uploads: %s", entry.Name())
		require.NotContains(t, entry.Name(), "post-pages", "the page dump folder is removed")
	}

	status := performLinkStatus(handler, created.JobID)
	require.Equal(t, http.StatusOK, status.Code)
	again := decodePostImport(t, status)
	require.Equal(t, "post", again.Kind)
	require.Equal(t, 3, again.ImageCount)

	repeated := performLinkImport(t, handler, map[string]any{"url": postLink, "external_id": "wa-msg-1"})
	require.Equal(t, http.StatusOK, repeated.Code)
	require.Equal(t, created.JobID, decodePostImport(t, repeated).JobID)
}

// A single-image post is the same, with one frame.
func TestASingleImagePostIsStoredAsAPost(t *testing.T) {
	handler, db, _ := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)
	_, cdn := installInstagramPost(t, handler, threeImageCarousel()[:1], false)

	response := performLinkImport(t, handler, map[string]any{"url": postLink})
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	created := decodePostImport(t, response)
	require.Equal(t, "post", created.Kind)
	require.Equal(t, 1, created.ImageCount)
	require.Len(t, cdn.urls(), 1)
	visuals := decodeVisuals(t, getVisuals(handler, created.JobID))
	require.Len(t, visuals.Frames, 1)
	require.Equal(t, "1/1", visuals.Frames[0].Label)
}

// In a carousel that mixes images and videos, a video item contributes its
// cover image, labelled as a video, so the post's order stays whole.
func TestAMixedCarouselKeepsEveryItemInOrder(t *testing.T) {
	handler, db, _ := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)
	items := threeImageCarousel()
	items[1].video = true
	installInstagramPost(t, handler, items, true)

	created := decodePostImport(t, performLinkImport(t, handler, map[string]any{"url": postLink}))
	require.Equal(t, "post", created.Kind)
	require.Equal(t, 3, created.ImageCount)
	visuals := decodeVisuals(t, getVisuals(handler, created.JobID))
	require.Equal(t, []string{"1/3", "2/3 video", "3/3"}, []string{visuals.Frames[0].Label, visuals.Frames[1].Label, visuals.Frames[2].Label})
}

// A page that carries no media is Instagram's login wall.
func TestAPostPageWithoutMediaAsksForALogin(t *testing.T) {
	handler, db, uploadDir := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)
	_, cdn := installInstagramPost(t, handler, nil, true)

	response := performLinkImport(t, handler, map[string]any{"url": postLink})
	require.Equal(t, http.StatusBadGateway, response.Code, response.Body.String())
	require.Equal(t, "instagram_login_required", decodeYouTubeError(t, response).Code)
	require.Empty(t, cdn.urls())
	require.Equal(t, int64(0), countJobs(t, db))
	entries, err := os.ReadDir(uploadDir)
	require.NoError(t, err)
	for _, entry := range entries {
		require.NotEqual(t, "visuals", entry.Name(), "nothing is left behind")
		require.NotContains(t, entry.Name(), "post-pages")
	}
}

// Image URLs come from a page fetched off the internet, so only Instagram's
// own CDN hosts over https are fetched; anything else fails the import.
func TestAPostImageOutsideInstagramsCDNIsRefused(t *testing.T) {
	handler, db, uploadDir := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)
	items := threeImageCarousel()
	items[2].imageURL = "https://internal.example/three.jpg"
	_, cdn := installInstagramPost(t, handler, items, true)

	response := performLinkImport(t, handler, map[string]any{"url": postLink})
	require.Equal(t, http.StatusBadGateway, response.Code, response.Body.String())
	require.Equal(t, "download_failed", decodeYouTubeError(t, response).Code)
	for _, requested := range cdn.urls() {
		parsed, err := url.Parse(requested)
		require.NoError(t, err)
		require.NotEqual(t, "internal.example", parsed.Host)
	}
	require.Equal(t, int64(0), countJobs(t, db))
	require.NoDirExists(t, filepath.Join(uploadDir, "visuals"))
}

// Instagram refusing before the page is read keeps its specific codes.
func TestAPostThatInstagramRefusesKeepsTheSpecificCode(t *testing.T) {
	cases := map[string]string{
		"ERROR: [Instagram] DfdtAkRG_g9: Unable to download webpage: HTTP Error 429: Too Many Requests": "rate_limited",
		"ERROR: [Instagram] DfdtAkRG_g9: Requested content is not available, rate-limit reached or login required. Use --cookies": "instagram_login_required",
	}
	for stderr, code := range cases {
		t.Run(code, func(t *testing.T) {
			handler, db, _ := newYouTubeTestHandler(t)
			createHouseProfiles(t, db)
			installInstagramPost(t, handler, threeImageCarousel(), true)
			t.Setenv("FAKE_POST_STDERR", stderr)

			response := performLinkImport(t, handler, map[string]any{"url": postLink})
			require.Equal(t, http.StatusBadGateway, response.Code, response.Body.String())
			require.Equal(t, code, decodeYouTubeError(t, response).Code)
		})
	}
}

// The import dialog takes an image post too: the preview says it is a post
// and how many images it has, and the import stores the post.
func TestTheDialogPreviewsAndImportsAnImagePost(t *testing.T) {
	handler, db, _ := newYouTubeTestHandler(t)
	_, english := createHouseProfiles(t, db)
	_, cdn := installInstagramPost(t, handler, threeImageCarousel(), true)

	preview := performPreviewRequest(t, handler, postLink)
	require.Equal(t, http.StatusOK, preview.Code, preview.Body.String())
	var previewBody struct {
		Title      string  `json:"title"`
		Channel    *string `json:"channel"`
		Kind       string  `json:"kind"`
		ImageCount int     `json:"image_count"`
	}
	require.NoError(t, json.Unmarshal(preview.Body.Bytes(), &previewBody))
	require.Equal(t, "post", previewBody.Kind)
	require.Equal(t, 3, previewBody.ImageCount)
	require.Equal(t, "Comment “AI” to get the link.", previewBody.Title)
	require.Equal(t, "@simplifyinai", *previewBody.Channel)
	require.Empty(t, cdn.urls(), "the preview downloads no images")

	imported := performYouTubeRequest(t, handler, map[string]any{"url": postLink, "profile_id": english.ID})
	require.Equal(t, http.StatusOK, imported.Code, imported.Body.String())
	var stored models.TranscriptionJob
	require.NoError(t, db.First(&stored).Error)
	require.Equal(t, "post", *stored.LinkKind)
	require.Equal(t, models.StatusCompleted, stored.Status)
}

// Videos keep reporting kind "video" and no images.
func TestAVideoImportReportsItsKind(t *testing.T) {
	installFakeSocialYtDlp(t, "ig")
	handler, db, _ := newYouTubeTestHandler(t)
	createHouseProfiles(t, db)

	created := decodePostImport(t, performLinkImport(t, handler, map[string]any{"url": "https://www.instagram.com/reel/ABC123xyz/"}))
	require.Equal(t, "video", created.Kind)
	require.Equal(t, 0, created.ImageCount)
}

// Rebuilding a post's visuals fetches its images again; deleting it removes
// its images, sheets and thumbnail.
func TestAPostsVisualsRebuildAndDelete(t *testing.T) {
	handler, db, uploadDir := newYouTubeTestHandler(t)
	handler.fileService = service.NewFileService()
	createHouseProfiles(t, db)
	_, cdn := installInstagramPost(t, handler, threeImageCarousel(), true)
	created := decodePostImport(t, performLinkImport(t, handler, map[string]any{"url": postLink}))

	accepted := performVisualsRequest(handler, http.MethodPost, "/visuals", gin.Params{{Key: "id", Value: created.JobID}}, handler.BuildJobVisuals)
	require.Equal(t, http.StatusAccepted, accepted.Code, accepted.Body.String())
	visuals := waitForVisuals(t, handler, created.JobID)
	require.Equal(t, "ready", visuals.Status)
	require.Len(t, visuals.Frames, 3)
	require.Len(t, cdn.urls(), 6, "the three images were fetched again")
	var stored models.TranscriptionJob
	require.NoError(t, db.First(&stored, "id = ?", created.JobID).Error)
	require.Equal(t, models.StatusCompleted, stored.Status)

	deleted := performVisualsRequest(handler, http.MethodDelete, "/api/v1/transcription/"+created.JobID, gin.Params{{Key: "id", Value: created.JobID}}, handler.DeleteTranscriptionJob)
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())
	require.NoDirExists(t, filepath.Join(uploadDir, "visuals", created.JobID))
	require.NoFileExists(t, *stored.ThumbnailPath)
}

// The image client sends every request through the link-import proxy.
func TestPostImagesGoThroughTheProxy(t *testing.T) {
	client := newPostImageClient("http://egress.example:8888", nil)
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	request, err := http.NewRequest(http.MethodGet, "https://scontent.cdninstagram.com/v/one.jpg", nil)
	require.NoError(t, err)
	proxyURL, err := transport.Proxy(request)
	require.NoError(t, err)
	require.Equal(t, "http://egress.example:8888", proxyURL.String())
	require.NotZero(t, client.Timeout)
}
