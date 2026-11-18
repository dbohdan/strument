package httpx

import (
	"bytes"
	"context"
	"io"
	"maps"
	"net/http"
)

// DefaultUserAgent is the user agent of a request whose caller names none.
// The binary passes "Strument/<version>"; this is for a client built without
// one, such as in a test.
const DefaultUserAgent = "Strument"

// Post sends body to url and reads the answer, at most limit bytes of it.
//
// It is the request the JSON services Strument calls have in common: the
// content type of the body, an Accept for JSON, the user agent, and whatever
// header adds, such as the service's key. Each service decodes the body and
// reads its own errors from it, so the answer comes back read but not
// interpreted.
func Post(ctx context.Context, client *http.Client, url, contentType string, body []byte,
	userAgent string, header http.Header, limit int64,
) (Reply, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Reply{}, err
	}
	maps.Copy(req.Header, header)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	if userAgent == "" {
		userAgent = DefaultUserAgent
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return Reply{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return Reply{}, err
	}
	return Reply{Status: resp.StatusCode, Header: resp.Header, Body: raw}, nil
}

// Reply is a response with its body read: nothing in it is left to close.
type Reply struct {
	Status int
	Header http.Header
	Body   []byte
}
