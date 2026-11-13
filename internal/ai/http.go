package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Post sends body as JSON to url with the given headers, retrying like Do, and
// returns the response body (at most 1 MiB) of a 200 answer. Any other status
// becomes a StatusError that never quotes the response, which can echo the prompt.
func Post(ctx context.Context, client *http.Client, label, url string, header map[string]string, body any) ([]byte, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	resp, err := Do(ctx, client, func() (*http.Request, error) {
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range header {
			req.Header.Set(k, v)
		}
		return req, nil
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, StatusError(label, resp.StatusCode)
	}
	return out, nil
}

// StatusError explains an HTTP failure in words a person can act on, without
// quoting the response body (which can echo the prompt).
func StatusError(label string, code int) error {
	switch code {
	case 400:
		return fmt.Errorf("%s rejected the request (HTTP 400): the model name may be wrong. In /models use  model N NAME", label)
	case 401, 403:
		return fmt.Errorf("%s rejected the API key (HTTP %d): check it with  key N  in /models", label, code)
	case 404:
		return fmt.Errorf("%s does not know this model (HTTP 404): it may have been retired. In /models use  model N NAME", label)
	case 429:
		return fmt.Errorf("%s says you are over your rate or quota limit (HTTP 429)", label)
	}
	return fmt.Errorf("%s answered HTTP %d", label, code)
}
