package llm

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type recordedOllama struct {
	server *httptest.Server
	bodies []map[string]any
}

func newRecordedOllama(t *testing.T, reply string) *recordedOllama {
	recorded := &recordedOllama{}
	recorded.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/version":
			_, _ = io.WriteString(writer, `{"version":"0.35.1"}`)
		case "/api/chat":
			var body map[string]any
			require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
			recorded.bodies = append(recorded.bodies, body)
			_, _ = io.WriteString(writer, `{"model":"`+body["model"].(string)+`","message":{"role":"assistant","content":"`+reply+`"},"done":true}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(recorded.server.Close)
	return recorded
}

func closedURL(t *testing.T) string {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	return "http://" + address
}

func TestParseOllamaEndpoints(t *testing.T) {
	endpoints, err := ParseOllamaEndpoints(" http://first:11434=qwen3:32b , http://second:11435=qwen3:4b-instruct ")
	require.NoError(t, err)
	require.Equal(t, []OllamaEndpoint{
		{BaseURL: "http://first:11434", Model: "qwen3:32b"},
		{BaseURL: "http://second:11435", Model: "qwen3:4b-instruct"},
	}, endpoints)

	_, err = ParseOllamaEndpoints("http://first:11434")
	require.Error(t, err)
}

func TestFailoverUsesTheFirstReachableEndpointAndItsModel(t *testing.T) {
	second := newRecordedOllama(t, "resumen")
	failover := NewOllamaFailover([]OllamaEndpoint{
		{BaseURL: closedURL(t), Model: "big-model"},
		{BaseURL: second.server.URL, Model: "small-model"},
	})

	response, err := failover.ChatCompletion(context.Background(), "model-the-caller-asked-for", []ChatMessage{{Role: "user", Content: "hola"}}, 0)

	require.NoError(t, err)
	require.Equal(t, "resumen", response.Choices[0].Message.Content)
	require.Equal(t, "small-model", response.Model)
	require.Len(t, second.bodies, 1)
	require.Equal(t, "small-model", second.bodies[0]["model"])

	models, err := failover.GetModels(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"small-model"}, models)
}

func TestFailoverPrefersTheFirstEndpointWhenItAnswers(t *testing.T) {
	first := newRecordedOllama(t, "uno")
	second := newRecordedOllama(t, "dos")
	failover := NewOllamaFailover([]OllamaEndpoint{
		{BaseURL: first.server.URL, Model: "big-model"},
		{BaseURL: second.server.URL, Model: "small-model"},
	})

	response, err := failover.ChatCompletion(context.Background(), "", []ChatMessage{{Role: "user", Content: "hola"}}, 0)

	require.NoError(t, err)
	require.Equal(t, "uno", response.Choices[0].Message.Content)
	require.Empty(t, second.bodies)
}

func TestFailoverFailsWhenNoEndpointAnswers(t *testing.T) {
	failover := NewOllamaFailover([]OllamaEndpoint{{BaseURL: closedURL(t), Model: "big-model"}})

	_, err := failover.ChatCompletion(context.Background(), "", []ChatMessage{{Role: "user", Content: "hola"}}, 0)

	require.ErrorContains(t, err, "no Ollama endpoint is reachable")
}

// Ollama silently drops whatever does not fit in num_ctx, and its default is
// far smaller than a long meeting.
func TestOllamaRequestsSizeTheContextToTheConversationAndSkipThinking(t *testing.T) {
	ollama := newRecordedOllama(t, "ok")
	service := NewOllamaService(ollama.server.URL)

	_, err := service.ChatCompletion(context.Background(), "model", []ChatMessage{{Role: "user", Content: "corto"}}, 0)
	require.NoError(t, err)
	_, err = service.ChatCompletion(context.Background(), "model", []ChatMessage{{Role: "user", Content: strings.Repeat("palabra ", 6500)}}, 0)
	require.NoError(t, err)

	short := ollama.bodies[0]["options"].(map[string]any)
	long := ollama.bodies[1]["options"].(map[string]any)
	require.Equal(t, float64(4096), short["num_ctx"])
	require.Equal(t, float64(20480), long["num_ctx"])
	require.Equal(t, false, ollama.bodies[0]["think"])
}
