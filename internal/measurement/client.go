package measurement

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"
)

type Client struct {
	Base string
	HTTP *http.Client
}

func New(base string) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{strings.TrimRight(base, "/"), &http.Client{Jar: jar, Timeout: 3 * time.Minute}}
}
func (c *Client) Request(method, path string, body any) ([]byte, time.Duration, error) {
	var data []byte
	var e error
	if body != nil {
		data, e = json.Marshal(body)
		if e != nil {
			return nil, 0, e
		}
	}
	req, e := http.NewRequest(method, c.Base+path, bytes.NewReader(data))
	if e != nil {
		return nil, 0, e
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	start := time.Now()
	res, e := c.HTTP.Do(req)
	if e != nil {
		return nil, time.Since(start), e
	}
	defer res.Body.Close()
	b, e := io.ReadAll(res.Body)
	d := time.Since(start)
	if e != nil {
		return b, d, e
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return b, d, fmt.Errorf("%s %s: HTTP %d: %s", method, path, res.StatusCode, b)
	}
	return b, d, nil
}
func (c *Client) Login(login, password string) error {
	_, _, e := c.Request("POST", "/api/login", map[string]string{"login": login, "password": password})
	return e
}
func (c *Client) Logout() error { _, _, e := c.Request("POST", "/api/logout", nil); return e }
