package llm

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// OllamaEndpointsEnv lists Ollama hosts in order of preference, each with the
// model it serves: "http://first:11434=model-a,http://second:11434=model-b".
// When set it replaces the stored LLM configuration.
const OllamaEndpointsEnv = "OLLAMA_ENDPOINTS"

// OllamaEndpoint is one Ollama host and the model to run there.
type OllamaEndpoint struct {
	BaseURL string
	Model   string
}

// ParseOllamaEndpoints reads the OLLAMA_ENDPOINTS format.
func ParseOllamaEndpoints(value string) ([]OllamaEndpoint, error) {
	var endpoints []OllamaEndpoint
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		baseURL, model, found := strings.Cut(entry, "=")
		if !found || strings.TrimSpace(baseURL) == "" || strings.TrimSpace(model) == "" {
			return nil, fmt.Errorf("ollama endpoint %q is not url=model", entry)
		}
		endpoints = append(endpoints, OllamaEndpoint{BaseURL: strings.TrimSpace(baseURL), Model: strings.TrimSpace(model)})
	}
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("no ollama endpoints configured")
	}
	return endpoints, nil
}

// OllamaFailoverFromEnvironment returns the failover service configured in
// OLLAMA_ENDPOINTS, or nil when the variable is unset.
func OllamaFailoverFromEnvironment() (*OllamaFailover, error) {
	value := strings.TrimSpace(os.Getenv(OllamaEndpointsEnv))
	if value == "" {
		return nil, nil
	}
	endpoints, err := ParseOllamaEndpoints(value)
	if err != nil {
		return nil, err
	}
	return NewOllamaFailover(endpoints), nil
}

// OllamaFailover sends each request to the first Ollama host that answers,
// with that host's own model: a big model on a GPU host that sleeps, a small
// one on a host that is always up. The caller's model choice is ignored,
// because a model name only means something on the host that has it.
type OllamaFailover struct {
	endpoints []OllamaEndpoint
	probe     *http.Client
}

func NewOllamaFailover(endpoints []OllamaEndpoint) *OllamaFailover {
	return &OllamaFailover{endpoints: endpoints, probe: &http.Client{Timeout: 3 * time.Second}}
}

// Endpoints lists the configured hosts as url=model, in order.
func (f *OllamaFailover) Endpoints() string {
	parts := make([]string, len(f.endpoints))
	for index, endpoint := range f.endpoints {
		parts[index] = endpoint.BaseURL + "=" + endpoint.Model
	}
	return strings.Join(parts, ",")
}

func (f *OllamaFailover) choose(ctx context.Context) (*OllamaService, string, error) {
	for _, endpoint := range f.endpoints {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint.BaseURL, "/")+"/api/version", nil)
		if err != nil {
			continue
		}
		response, err := f.probe.Do(request)
		if err != nil {
			continue
		}
		response.Body.Close()
		if response.StatusCode == http.StatusOK {
			return NewOllamaService(endpoint.BaseURL), endpoint.Model, nil
		}
	}
	return nil, "", fmt.Errorf("no Ollama endpoint is reachable (tried %s)", f.Endpoints())
}

func (f *OllamaFailover) GetModels(ctx context.Context) ([]string, error) {
	_, model, err := f.choose(ctx)
	if err != nil {
		return nil, err
	}
	return []string{model}, nil
}

func (f *OllamaFailover) ChatCompletion(ctx context.Context, _ string, messages []ChatMessage, temperature float64) (*ChatResponse, error) {
	service, model, err := f.choose(ctx)
	if err != nil {
		return nil, err
	}
	return service.ChatCompletion(ctx, model, messages, temperature)
}

func (f *OllamaFailover) ChatCompletionStream(ctx context.Context, _ string, messages []ChatMessage, temperature float64) (<-chan string, <-chan error) {
	service, model, err := f.choose(ctx)
	if err != nil {
		// The content channel stays open so readers see the error, not an
		// empty but successful stream.
		errorChan := make(chan error, 1)
		errorChan <- err
		return make(chan string), errorChan
	}
	return service.ChatCompletionStream(ctx, model, messages, temperature)
}

func (f *OllamaFailover) GetContextWindow(ctx context.Context, _ string) (int, error) {
	service, model, err := f.choose(ctx)
	if err != nil {
		return 0, err
	}
	return service.GetContextWindow(ctx, model)
}
