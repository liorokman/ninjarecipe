package harness

import (
	"io"
	"net/http"
	"net/url"
	"strings"
)

type urlT = url.URL

func parseURL(s string) (*url.URL, error) { return url.Parse(s) }

// Do sends a request relative to the base URL.
func (c *Client) Do(method, path string, body io.Reader, hdr map[string]string) (*http.Response, string) {
	req, err := http.NewRequest(method, c.Base+path, body)
	if err != nil {
		panic(err)
	}
	if c.Bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.Bearer)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

// Get is a GET.
func (c *Client) Get(path string, hdr ...string) (*http.Response, string) {
	return c.Do("GET", path, nil, hmap(hdr))
}

// PostForm posts urlencoded values (Origin set so CrossOriginProtection passes as a browser same-origin request).
func (c *Client) PostForm(path string, v url.Values, hdr ...string) (*http.Response, string) {
	h := hmap(hdr)
	if h == nil {
		h = map[string]string{}
	}
	h["Content-Type"] = "application/x-www-form-urlencoded"
	if _, ok := h["Sec-Fetch-Site"]; !ok {
		h["Sec-Fetch-Site"] = "same-origin"
	}
	return c.Do("POST", path, strings.NewReader(v.Encode()), h)
}

func hmap(kv []string) map[string]string {
	if len(kv) == 0 {
		return nil
	}
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}
