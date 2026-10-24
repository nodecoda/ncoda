package host

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAdapterBuildMethodDefault(t *testing.T) {
	built := Adapter{}.Build(Request{URL: "http://x/p"})
	if built.Method != http.MethodGet {
		t.Fatalf("Method = %q, want GET", built.Method)
	}
	built = Adapter{}.Build(Request{Method: "POST", URL: "http://x/p"})
	if built.Method != "POST" {
		t.Fatalf("Method = %q, want POST", built.Method)
	}
}

func TestAdapterBuildHeaders(t *testing.T) {
	t.Run("ordered set last wins", func(t *testing.T) {
		built := Adapter{}.Build(Request{Headers: []KV{
			{Name: "X-A", Value: "one"},
			{Name: "x-requested-with", Value: "XMLHttpRequest"},
			{Name: "X-A", Value: "two"},
		}})
		if got := built.Headers.Get("X-A"); got != "two" {
			t.Errorf("X-A = %q, want two (ordered Set)", got)
		}
		if got := built.Headers.Get("X-Requested-With"); got != "XMLHttpRequest" {
			t.Errorf("X-Requested-With = %q", got)
		}
	})

	t.Run("explicit content-type wins verbatim", func(t *testing.T) {
		const ct = "application/x-www-form-urlencoded; charset=UTF-8"
		built := Adapter{}.Build(Request{
			Body:    `{"k":"v"}`,
			Headers: []KV{{Name: "content-type", Value: ct}},
		})
		if got := built.Headers.Get("Content-Type"); got != ct {
			t.Errorf("Content-Type = %q, want %q", got, ct)
		}
	})

	t.Run("body defaults to application/json", func(t *testing.T) {
		built := Adapter{}.Build(Request{Body: `{"k":"v"}`})
		if got := built.Headers.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
	})

	t.Run("no body no content-type", func(t *testing.T) {
		built := Adapter{}.Build(Request{})
		if got := built.Headers.Get("Content-Type"); got != "" {
			t.Errorf("Content-Type = %q, want empty", got)
		}
	})
}

func TestAdapterBuildParams(t *testing.T) {
	cases := []struct {
		name   string
		url    string
		params []KV
		want   string
	}{
		{
			name:   "append in author order",
			url:    "http://x/p",
			params: []KV{{Name: "z", Value: "1"}, {Name: "a", Value: "2"}},
			want:   "http://x/p?z=1&a=2",
		},
		{
			name:   "existing key not overridden or duplicated",
			url:    "http://x/p?a=1",
			params: []KV{{Name: "b", Value: "2"}, {Name: "a", Value: "9"}},
			want:   "http://x/p?a=1&b=2",
		},
		{
			name:   "presence check is case sensitive",
			url:    "http://x/p?A=1",
			params: []KV{{Name: "a", Value: "2"}},
			want:   "http://x/p?A=1&a=2",
		},
		{
			name:   "existing query bytes kept verbatim",
			url:    "http://x/p?b=%2F+raw&a=1",
			params: []KV{{Name: "c", Value: "3"}},
			want:   "http://x/p?b=%2F+raw&a=1&c=3",
		},
		{
			name:   "fragment preserved",
			url:    "http://x/p?a=1#frag",
			params: []KV{{Name: "b", Value: "2"}},
			want:   "http://x/p?a=1&b=2#frag",
		},
		{
			name:   "values are encoded",
			url:    "http://x/p",
			params: []KV{{Name: "q", Value: "a b&c"}},
			want:   "http://x/p?q=a+b%26c",
		},
		{
			name:   "no params leaves url untouched",
			url:    "http://x/p?a=1",
			params: nil,
			want:   "http://x/p?a=1",
		},
		{
			name:   "all params already present leaves url untouched",
			url:    "http://x/p?a=1",
			params: []KV{{Name: "a", Value: "9"}},
			want:   "http://x/p?a=1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Adapter{}.Build(Request{URL: tc.url, Params: tc.params}).URL
			if got != tc.want {
				t.Fatalf("URL = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAdapterClientFollowRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("final"))
	}))
	defer srv.Close()

	const timeout = 5 * time.Second
	follow := Adapter{FollowRedirects: true}.Client(timeout)
	if follow.Timeout != timeout {
		t.Fatalf("Timeout = %v, want %v", follow.Timeout, timeout)
	}
	resp, err := follow.Get(srv.URL + "/start")
	if err != nil {
		t.Fatalf("follow get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "final" {
		t.Fatalf("follow = %d %q, want 200 final", resp.StatusCode, body)
	}

	stop := Adapter{FollowRedirects: false}.Client(timeout)
	resp, err = stop.Get(srv.URL + "/start")
	if err != nil {
		t.Fatalf("no-follow get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("no-follow status = %d, want 302", resp.StatusCode)
	}
}
