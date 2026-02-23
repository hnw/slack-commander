package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type httpRunner struct {
	cfg *CommandConfig
}

// NewHTTPRunner returns a runner backed by net/http.
func NewHTTPRunner(cfg *CommandConfig) CommandRunner {
	return &httpRunner{cfg: cfg}
}

func (r *httpRunner) CommandContext(ctx context.Context, _ string, arg ...string) Cmd {
	if ctx == nil {
		panic("nil Context")
	}
	wildcard := ""
	hasWildcard := false
	if len(arg) > 0 {
		hasWildcard = true
		wildcard = arg[0]
	}
	return &httpCmd{
		ctx:         ctx,
		cfg:         r.cfg,
		wildcard:    wildcard,
		hasWildcard: hasWildcard,
	}
}

type httpCmd struct {
	ctx         context.Context
	cfg         *CommandConfig
	wildcard    string
	hasWildcard bool
	stdin       io.Reader
	stdout      io.Writer
	stderr      io.Writer
}

func (c *httpCmd) SetStdin(r io.Reader) {
	c.stdin = r
}

func (c *httpCmd) SetStdout(w io.Writer) {
	c.stdout = w
}

func (c *httpCmd) SetStderr(w io.Writer) {
	c.stderr = w
}

func (c *httpCmd) Run(timeout int) int {
	if err := c.validateConfig(); err != nil {
		c.writeErr(err)
		return 127
	}

	req, err := c.buildRequest()
	if err != nil {
		c.writeErr(err)
		return 127
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return c.handleRequestError(err, timeout)
	}
	defer c.closeBody(resp.Body)

	return c.handleResponse(resp)
}

func (c *httpCmd) validateConfig() error {
	if c.cfg == nil || c.cfg.Definition == nil {
		return errors.New("http config is nil")
	}
	if strings.TrimSpace(c.cfg.URL) == "" {
		return errors.New("url is required for http runner")
	}
	return nil
}

func (c *httpCmd) buildRequest() (*http.Request, error) {
	method := strings.ToUpper(strings.TrimSpace(c.cfg.Method))
	if method == "" {
		method = "POST"
	}
	urlStr := c.expandWildcard(c.cfg.URL)
	body := c.expandWildcard(c.cfg.Body)

	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(c.ctx, method, urlStr, bodyReader)
	if err != nil {
		return nil, err
	}

	for k, v := range c.cfg.Headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		req.Header.Set(k, c.expandWildcard(v))
	}

	return req, nil
}

func (c *httpCmd) expandWildcard(value string) string {
	if !c.hasWildcard {
		return value
	}
	return strings.Replace(value, "*", c.wildcard, 1)
}

func (c *httpCmd) handleRequestError(err error, timeout int) int {
	if timeout > 0 && c.ctx != nil && errors.Is(c.ctx.Err(), context.DeadlineExceeded) {
		c.writeTimeout(timeout)
		return 143
	}
	c.writeErr(err)
	return 127
}

func (c *httpCmd) handleResponse(resp *http.Response) int {
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		c.writeErr(err)
		return 127
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if c.stdout != nil && len(data) > 0 {
			_, _ = c.stdout.Write(data)
		}
		return 0
	}
	if c.stderr != nil {
		if len(data) > 0 {
			_, _ = c.stderr.Write(data)
		} else {
			_, _ = fmt.Fprintf(c.stderr, "%s", resp.Status)
		}
	}
	return 1
}

func (c *httpCmd) closeBody(body io.ReadCloser) {
	if err := body.Close(); err != nil {
		c.writeErr(err)
	}
}

func (c *httpCmd) writeErr(err error) {
	if c.stderr != nil {
		_, _ = fmt.Fprintf(c.stderr, "Error: %v", err)
	}
}

func (c *httpCmd) writeTimeout(timeout int) {
	if c.stderr != nil {
		_, _ = fmt.Fprintf(c.stderr, "Timeout exceeded (%ds)", timeout)
	}
}
