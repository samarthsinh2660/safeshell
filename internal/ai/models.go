package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// ModelInfo is one model a provider offers.
type ModelInfo struct {
	ID   string
	Name string // display name when the service gives one
}

// FetchModels reads the model list from url. Both Anthropic and the OpenAI
// style APIs answer {"data":[{"id":...}]}. header holds the key headers. The
// response body is never shown in an error, because it can echo the request.
func FetchModels(ctx context.Context, client *http.Client, label, url string, header map[string]string) ([]ModelInfo, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("could not reach " + label + ": check your network connection")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case 401, 403, 429:
			return nil, StatusError(label, resp.StatusCode)
		}
		return nil, fmt.Errorf("%s could not list its models (HTTP %d)", label, resp.StatusCode)
	}
	var out struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out) != nil {
		return nil, errors.New(label + " sent a model list that could not be read")
	}
	var list []ModelInfo
	for _, m := range out.Data {
		if m.ID != "" {
			list = append(list, ModelInfo{ID: m.ID, Name: m.DisplayName})
		}
	}
	return list, nil
}
