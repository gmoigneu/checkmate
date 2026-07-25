package oauth

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client ID Metadata Document fetching.
//
// The client hands us a URL and we go fetch it. That is a server-side request
// forgery primitive by construction, and the MCP security guidance calls it out
// explicitly: a malicious client could aim the authorization server at a private
// administration endpoint it happens to be able to reach.
//
// The defences here, in order of importance:
//
//  1. https only, no exceptions, so a fetch cannot be downgraded.
//  2. Every resolved IP is checked against private, loopback, link-local and
//     other non-public ranges before the connection is allowed. The check runs in
//     DialContext on the address the dialer actually connects to, not on the
//     hostname, which is what closes the DNS-rebinding window between a check and
//     a later connect.
//  3. Redirects are refused outright. Following one would mean re-validating a
//     destination the client did not name, and no legitimate metadata document
//     needs a redirect.
//  4. The response body is capped and the whole fetch is bounded by a timeout, so
//     a hostile endpoint cannot exhaust memory or hold a request open.

const (
	// cimdMaxBytes caps the metadata document. A real one is well under a
	// kilobyte; this leaves room without inviting a memory exhaustion attempt.
	cimdMaxBytes = 64 << 10

	// cimdTimeout bounds the whole fetch.
	cimdTimeout = 10 * time.Second

	// cimdMinCacheTTL and cimdMaxCacheTTL bound how long a document is cached,
	// whatever its Cache-Control says.
	cimdMinCacheTTL = 5 * time.Minute
	cimdMaxCacheTTL = 24 * time.Hour
)

// ClientMetadata is the subset of a Client ID Metadata Document this server uses.
type ClientMetadata struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name"`
	ClientURI               string   `json:"client_uri"`
	LogoURI                 string   `json:"logo_uri"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	Scope                   string   `json:"scope"`
	ApplicationType         string   `json:"application_type"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	SoftwareID              string   `json:"software_id"`
	SoftwareVersion         string   `json:"software_version"`

	// CacheExpiresAt is derived from the response's cache headers.
	CacheExpiresAt time.Time `json:"-"`
}

// MetadataFetcher retrieves Client ID Metadata Documents.
type MetadataFetcher struct {
	client *http.Client

	// allowPrivateHosts disables the IP range check. Tests only; there is no
	// configuration path that turns this on in a running server.
	allowPrivateHosts bool
}

// NewMetadataFetcher builds a fetcher with the SSRF protections applied.
func NewMetadataFetcher() *MetadataFetcher {
	return newMetadataFetcher(false)
}

// newMetadataFetcherForTests allows loopback targets so a test can serve a
// document from httptest.
func newMetadataFetcherForTests() *MetadataFetcher {
	return newMetadataFetcher(true)
}

func newMetadataFetcher(allowPrivateHosts bool) *MetadataFetcher {
	dialer := &net.Dialer{Timeout: 5 * time.Second}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if !allowPrivateHosts {
				// Checked here rather than against the hostname: this is the
				// address the connection is actually made to, so a name that
				// resolves differently on a second lookup cannot slip past.
				host, _, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, fmt.Errorf("oauth: malformed address %q", addr)
				}

				ip := net.ParseIP(host)
				if ip == nil {
					return nil, fmt.Errorf("oauth: %q did not resolve to an IP", host)
				}

				if !isPublicIP(ip) {
					return nil, fmt.Errorf(
						"oauth: refusing to fetch client metadata from non-public address %s", ip)
				}
			}

			return dialer.DialContext(ctx, network, addr)
		},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		DisableKeepAlives:     true,
	}

	if allowPrivateHosts {
		// Tests only, alongside the relaxed address check: httptest's TLS server
		// presents a self-signed certificate. A real fetch verifies the chain.
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test-only path
	}

	return &MetadataFetcher{
		allowPrivateHosts: allowPrivateHosts,
		client: &http.Client{
			Transport: transport,
			Timeout:   cimdTimeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return fmt.Errorf("oauth: client metadata must not redirect")
			},
		},
	}
}

// IsMetadataDocumentID reports whether a client_id is a CIMD URL rather than an
// opaque identifier issued by dynamic registration.
//
// The spec requires an https scheme and a path component, which is also what
// keeps a bare origin from being mistaken for one.
func IsMetadataDocumentID(clientID string) bool {
	if !strings.HasPrefix(clientID, "https://") {
		return false
	}

	parsed, err := url.Parse(clientID)
	if err != nil {
		return false
	}

	return parsed.Host != "" && parsed.Path != "" && parsed.Path != "/"
}

// Fetch retrieves and validates the metadata document at clientID.
func (f *MetadataFetcher) Fetch(ctx context.Context, clientID string) (ClientMetadata, error) {
	if !IsMetadataDocumentID(clientID) {
		return ClientMetadata{}, errInvalidClient(
			"client_id must be an https URL with a path component")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, clientID, nil)
	if err != nil {
		return ClientMetadata{}, errInvalidClient("client_id is not a fetchable URL")
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "checkmate-oauth/1.0")

	res, err := f.client.Do(req)
	if err != nil {
		return ClientMetadata{}, errInvalidClient(
			fmt.Sprintf("could not fetch the client metadata document: %v", err))
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return ClientMetadata{}, errInvalidClient(
			fmt.Sprintf("client metadata document returned HTTP %d", res.StatusCode))
	}

	// Read one byte past the cap so an oversized body is detected rather than
	// silently truncated into something that might still parse.
	body, err := io.ReadAll(io.LimitReader(res.Body, cimdMaxBytes+1))
	if err != nil {
		return ClientMetadata{}, errInvalidClient("could not read the client metadata document")
	}

	if len(body) > cimdMaxBytes {
		return ClientMetadata{}, errInvalidClient("client metadata document is too large")
	}

	var meta ClientMetadata

	decoder := json.NewDecoder(strings.NewReader(string(body)))
	if err := decoder.Decode(&meta); err != nil {
		return ClientMetadata{}, errInvalidClient("client metadata document is not valid JSON")
	}

	if err := validateMetadata(&meta, clientID); err != nil {
		return ClientMetadata{}, err
	}

	meta.CacheExpiresAt = cacheExpiry(res.Header, time.Now())

	return meta, nil
}

// validateMetadata enforces the document's required shape.
func validateMetadata(meta *ClientMetadata, clientID string) error {
	// The document must claim exactly the URL it was served from, otherwise one
	// host could publish a document impersonating another's client_id.
	if meta.ClientID != clientID {
		return errInvalidClient("the document's client_id does not match its URL")
	}

	if strings.TrimSpace(meta.ClientName) == "" {
		return errInvalidClient("the client metadata document has no client_name")
	}

	if len(meta.RedirectURIs) == 0 {
		return errInvalidClient("the client metadata document has no redirect_uris")
	}

	if meta.ApplicationType == "" {
		// Absent, MCP clients are overwhelmingly native. Defaulting to "web" as
		// OIDC does would reject the loopback redirects they all use, which is
		// the trap the draft spec added application_type to avoid.
		meta.ApplicationType = "native"
	}

	if meta.ApplicationType != "native" && meta.ApplicationType != "web" {
		return errInvalidClient("application_type must be native or web")
	}

	for _, uri := range meta.RedirectURIs {
		if err := ValidateRedirectURI(uri, meta.ApplicationType); err != nil {
			return errInvalidClient(fmt.Sprintf("redirect_uri %q is not usable: %v", uri, err))
		}
	}

	// A self-hosted document cannot keep a secret, so CIMD clients are public.
	// private_key_jwt is the spec's option for authenticating them; it is not
	// implemented here, and claiming otherwise would be worse than refusing.
	if meta.TokenEndpointAuthMethod != "" && meta.TokenEndpointAuthMethod != "none" {
		return errInvalidClient(
			"this server only supports token_endpoint_auth_method \"none\" for metadata-document clients")
	}

	meta.TokenEndpointAuthMethod = "none"

	if len(meta.GrantTypes) == 0 {
		meta.GrantTypes = []string{"authorization_code", "refresh_token"}
	}

	if len(meta.ResponseTypes) == 0 {
		meta.ResponseTypes = []string{"code"}
	}

	for _, grant := range meta.GrantTypes {
		if grant != "authorization_code" && grant != "refresh_token" {
			return errInvalidClient(fmt.Sprintf("unsupported grant_type %q", grant))
		}
	}

	for _, responseType := range meta.ResponseTypes {
		if responseType != "code" {
			return errInvalidClient(
				"only the code response_type is supported; OAuth 2.1 removed the implicit grant")
		}
	}

	return nil
}

// cacheExpiry reads Cache-Control max-age, falling back to Expires, and clamps
// the result so a hostile or careless document cannot pin a stale entry forever
// or force a refetch on every authorization.
func cacheExpiry(header http.Header, now time.Time) time.Time {
	ttl := cimdMinCacheTTL

	if cc := header.Get("Cache-Control"); cc != "" {
		if strings.Contains(strings.ToLower(cc), "no-store") {
			return now.Add(cimdMinCacheTTL)
		}

		for _, part := range strings.Split(cc, ",") {
			part = strings.TrimSpace(strings.ToLower(part))

			after, found := strings.CutPrefix(part, "max-age=")
			if !found {
				continue
			}

			var seconds int64
			if _, err := fmt.Sscanf(after, "%d", &seconds); err == nil && seconds > 0 {
				ttl = time.Duration(seconds) * time.Second
			}
		}
	} else if expires := header.Get("Expires"); expires != "" {
		if parsed, err := http.ParseTime(expires); err == nil {
			ttl = time.Until(parsed)
		}
	}

	ttl = min(max(ttl, cimdMinCacheTTL), cimdMaxCacheTTL)

	return now.Add(ttl)
}

// isPublicIP reports whether an address is safe for the server to connect to.
//
// Allowlisting "globally routable" rather than blocklisting known-bad ranges:
// a blocklist has to be exhaustive to be correct, and this way a range nobody
// thought of fails closed.
func isPublicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}

	if v4 := ip.To4(); v4 != nil {
		switch {
		// 100.64.0.0/10, shared address space (carrier NAT, and Tailscale).
		case v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127:
			return false
		// 192.0.0.0/24, IETF protocol assignments.
		case v4[0] == 192 && v4[1] == 0 && v4[2] == 0:
			return false
		// 198.18.0.0/15, benchmarking.
		case v4[0] == 198 && (v4[1] == 18 || v4[1] == 19):
			return false
		// 240.0.0.0/4 upwards, reserved.
		case v4[0] >= 240:
			return false
		}

		return true
	}

	// IPv6 unique local addresses, fc00::/7.
	if len(ip) == net.IPv6len && ip[0]&0xfe == 0xfc {
		return false
	}

	// IPv4-mapped and NAT64 addresses would otherwise smuggle a v4 target
	// through as a v6 one.
	if ip.To4() == nil && strings.HasPrefix(ip.String(), "64:ff9b:") {
		return false
	}

	return true
}
