// Package host adapts ordered platform HTTP host fields (headers and query
// parameters) onto Go's net/http wire shapes.
//
// It is deliberately separate from internal/runtime/boundary: boundary owns
// value shaping/fill, host owns the physical request carrier. Build is a pure
// mapping (no I/O); Client only configures *http.Client.
package host

import (
	"net/http"
	"net/url"
	"strings"
	"time"
)

// KV is one ordered header or query entry.
type KV struct {
	Name  string
	Value string
}

// Request is the host-neutral request shape produced by the http node after
// template rendering.
type Request struct {
	Method  string
	URL     string
	Headers []KV
	Params  []KV
	Body    string
}

// Built is the wire-ready request: URL with appended query, resolved headers
// (possibly carrying Content-Type), and body.
type Built struct {
	Method  string
	URL     string
	Headers http.Header
	Body    string
}

// Adapter maps host fields onto wire fields. FollowRedirects=true follows
// redirects; false stops after the first response (returning it with
// http.ErrUseLastResponse from CheckRedirect).
type Adapter struct {
	FollowRedirects bool
}

// Build is a pure mapping from Request to Built; it never performs I/O.
func (a Adapter) Build(req Request) Built {
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	h := make(http.Header, len(req.Headers)+1)
	hasContentType := false
	// Ordered Set: a later duplicate name overrides an earlier one, exactly
	// as the source order declares.
	for _, kv := range req.Headers {
		h.Set(kv.Name, kv.Value)
		if strings.EqualFold(kv.Name, "content-type") {
			hasContentType = true
		}
	}
	// Content-Type priority: the source header (verbatim) wins; otherwise a
	// non-empty body gets application/json; otherwise no Content-Type.
	if req.Body != "" && !hasContentType {
		h.Set("Content-Type", "application/json")
	}
	return Built{
		Method:  method,
		URL:     appendParams(req.URL, req.Params),
		Headers: h,
		Body:    req.Body,
	}
}

// appendParams appends only Params whose name (case-sensitive) is not already
// present in the URL query. Existing query bytes are never re-encoded; new
// pairs are encoded in Params order.
func appendParams(rawURL string, params []KV) string {
	if len(params) == 0 {
		return rawURL
	}
	base, frag := rawURL, ""
	if i := strings.IndexByte(rawURL, '#'); i >= 0 {
		base, frag = rawURL[:i], rawURL[i:]
	}
	path, existing := base, ""
	if i := strings.IndexByte(base, '?'); i >= 0 {
		path, existing = base[:i], base[i+1:]
	}
	present := map[string]bool{}
	if existing != "" {
		// Presence-only parse: the returned URL keeps existing bytes verbatim.
		vals, _ := url.ParseQuery(existing)
		for k := range vals {
			present[k] = true
		}
	}
	var extra []string
	for _, kv := range params {
		if present[kv.Name] {
			continue
		}
		// Encode one pair at a time so Params declaration order survives
		// (url.Values.Encode sorts keys when handed a whole map).
		extra = append(extra, url.Values{kv.Name: []string{kv.Value}}.Encode())
	}
	if len(extra) == 0 {
		return rawURL
	}
	suffix := strings.Join(extra, "&")
	if existing == "" {
		return path + "?" + suffix + frag
	}
	return path + "?" + existing + "&" + suffix + frag
}

// Client returns an *http.Client with the given timeout. When
// FollowRedirects is false redirects are not followed: CheckRedirect returns
// http.ErrUseLastResponse so the first response is returned to the caller.
func (a Adapter) Client(timeout time.Duration) *http.Client {
	c := &http.Client{Timeout: timeout}
	if !a.FollowRedirects {
		c.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	return c
}
