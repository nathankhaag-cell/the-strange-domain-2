// Package update asks GitHub whether a newer release of the node exists.
//
// The check is advisory only: it never downloads or installs anything, and
// any failure (no internet, a mesh network, GitHub down or rate-limited) is
// ignored, so it never gets in the way of running a node offline.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// LatestURL is GitHub's "latest release" endpoint for this project.
const LatestURL = "https://api.github.com/repos/nathankhaag-cell/the-strange-domain-2/releases/latest"

// Status is what the last successful check found.
type Status struct {
	Current   string    `json:"current"`
	Latest    string    `json:"latest"`
	URL       string    `json:"url"` // the release page
	Available bool      `json:"available"`
	CheckedAt time.Time `json:"checked_at"`
}

// Checker checks for a newer release now and then and remembers the result.
type Checker struct {
	Current string       // the running version, such as v0.1.0
	URL     string       // defaults to LatestURL
	Client  *http.Client // defaults to one with a short timeout
	Log     *slog.Logger // defaults to slog.Default()

	status atomic.Pointer[Status]
}

// Enabled reports whether the running version can be compared at all.
// Development and CI builds ("dev", a commit hash) are not checked.
func (c *Checker) Enabled() bool {
	_, ok := Parse(c.Current)
	return ok
}

// Status returns the last result, or nil if no check has succeeded.
func (c *Checker) Status() *Status {
	if c == nil {
		return nil
	}
	return c.status.Load()
}

// Run checks once now and then every interval until ctx is done.
func (c *Checker) Run(ctx context.Context, interval time.Duration) {
	if !c.Enabled() {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		c.CheckAndLog(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// CheckAndLog runs one check and logs a line when a newer version exists.
// Errors are logged at debug level only.
func (c *Checker) CheckAndLog(ctx context.Context) {
	log := c.Log
	if log == nil {
		log = slog.Default()
	}
	st, err := c.Check(ctx)
	if err != nil {
		log.Debug("update check failed", "err", err)
		return
	}
	if st.Available {
		log.Info("a newer version of the node is available", "running", st.Current, "latest", st.Latest, "download", st.URL)
	}
}

// Check asks GitHub for the latest release and stores the result.
func (c *Checker) Check(ctx context.Context) (*Status, error) {
	cur, ok := Parse(c.Current)
	if !ok {
		return nil, errors.New("running version is not a release version")
	}
	url := c.URL
	if url == "" {
		url = LatestURL
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "strange-domain-node/"+c.Current)
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github answered %s", res.Status)
	}
	var rel struct {
		TagName    string `json:"tag_name"`
		HTMLURL    string `json:"html_url"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&rel); err != nil {
		return nil, err
	}
	latest, ok := Parse(rel.TagName)
	if !ok {
		return nil, fmt.Errorf("unexpected tag %q", rel.TagName)
	}
	st := &Status{
		Current:   c.Current,
		Latest:    rel.TagName,
		URL:       rel.HTMLURL,
		Available: !rel.Draft && !rel.Prerelease && Compare(latest, cur) > 0,
		CheckedAt: time.Now().UTC(),
	}
	if !strings.HasPrefix(st.URL, "https://github.com/") {
		st.URL = "https://github.com/nathankhaag-cell/the-strange-domain-2/releases/latest"
	}
	c.status.Store(st)
	return st, nil
}

// Version is a parsed semantic version.
type Version struct {
	Major, Minor, Patch int
	Pre                 string // "" for a release
}

// Parse reads "v1.2.3", "1.2.3" or "1.2.3-beta.1". Build metadata ("+...")
// is ignored.
func Parse(s string) (Version, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	s, _, _ = strings.Cut(s, "+")
	core, pre, _ := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	var n [3]int
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 || p == "" || (len(p) > 1 && p[0] == '0') {
			return Version{}, false
		}
		n[i] = v
	}
	return Version{n[0], n[1], n[2], pre}, true
}

// Compare returns -1, 0 or 1 following semver precedence.
func Compare(a, b Version) int {
	for _, d := range [3]int{a.Major - b.Major, a.Minor - b.Minor, a.Patch - b.Patch} {
		if d != 0 {
			return sign(d)
		}
	}
	switch {
	case a.Pre == b.Pre:
		return 0
	case a.Pre == "":
		return 1
	case b.Pre == "":
		return -1
	}
	ap, bp := strings.Split(a.Pre, "."), strings.Split(b.Pre, ".")
	for i := 0; i < len(ap) && i < len(bp); i++ {
		if ap[i] == bp[i] {
			continue
		}
		ai, aerr := strconv.Atoi(ap[i])
		bi, berr := strconv.Atoi(bp[i])
		switch {
		case aerr == nil && berr == nil:
			return sign(ai - bi)
		case aerr == nil:
			return -1 // numeric identifiers sort before alphanumeric ones
		case berr == nil:
			return 1
		}
		return sign(strings.Compare(ap[i], bp[i]))
	}
	return sign(len(ap) - len(bp))
}

func sign(d int) int {
	switch {
	case d > 0:
		return 1
	case d < 0:
		return -1
	}
	return 0
}
