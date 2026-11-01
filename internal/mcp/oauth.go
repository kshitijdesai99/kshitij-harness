package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// OAuthOptions enables generic public-client OAuth for challenged HTTP servers.
// Cached grants and refresh work without OpenBrowser; only initial connection
// startup may invoke it. Notice receives fixed, credential-free messages.
type OAuthOptions struct {
	StoreDir    string
	OpenBrowser func(context.Context, string) error
	Notice      func(string)
}

type oauthRequired struct {
	auth      *oauthAuth
	challenge []string
	rejected  string
}

func (*oauthRequired) Error() string {
	return "MCP OAuth authorization required; reconnect from an interactive terminal"
}

type oauthMetadata struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	RegistrationEndpoint  string   `json:"registration_endpoint"`
	CodeChallengeMethods  []string `json:"code_challenge_methods_supported"`
	TokenAuthMethods      []string `json:"token_endpoint_auth_methods_supported"`
	ResponseTypes         []string `json:"response_types_supported"`
	IssParameterSupported bool     `json:"authorization_response_iss_parameter_supported"`
}

type oauthAuth struct {
	options  OAuthOptions
	endpoint string
	local    bool
	client   *http.Client
	mu       sync.Mutex    // Protects only access and expires; never held during I/O.
	gate     chan struct{} // Serializes discovery and credential work with cancellation.
	metadata *oauthMetadata
	resource string
	scope    string
	access   string
	expires  time.Time
}

func (t *httpTransport) enableOAuth(options OAuthOptions) {
	for key := range t.headers {
		if strings.EqualFold(key, "Authorization") {
			return
		}
	}
	u, _ := url.Parse(t.endpoint)
	a := &oauthAuth{options: options, endpoint: t.endpoint, local: literalLoopback(u.Hostname()), gate: make(chan struct{}, 1)}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil // Do not delegate DNS or destination policy to an ambient proxy.
	tr.DialContext = a.dial
	a.client = &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("OAuth redirects disabled") }}
	t.oauth = a
}

func literalLoopback(host string) bool { ip := net.ParseIP(host); return ip != nil && ip.IsLoopback() }

var nonPublicOAuthIPv6 = []netip.Prefix{
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("fec0::/10"),
	netip.MustParsePrefix("3fff::/20"),
}

func publicOAuthIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		// Shared-address space can expose provider metadata services; these
		// and reserved/documentation/benchmark ranges are not public targets.
		if v4[0] == 0 || v4[0] >= 240 || (v4[0] == 100 && v4[1]&0xc0 == 64) ||
			(v4[0] == 192 && v4[1] == 0 && (v4[2] == 0 || v4[2] == 2)) ||
			(v4[0] == 198 && (v4[1] == 18 || v4[1] == 19 || (v4[1] == 51 && v4[2] == 100))) ||
			(v4[0] == 203 && v4[1] == 0 && v4[2] == 113) {
			return false
		}
	} else if addr, ok := netip.AddrFromSlice(ip); ok {
		for _, prefix := range nonPublicOAuthIPv6 {
			if prefix.Contains(addr) {
				return false
			}
		}
	}
	return true
}
func (a *oauthAuth) validateURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && a.local && literalLoopback(u.Hostname()))) {
		return nil, fmt.Errorf("MCP OAuth requires secure endpoint URLs")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && !publicOAuthIP(ip) && !(a.local && ip.IsLoopback()) {
		return nil, fmt.Errorf("MCP OAuth destination is not public")
	}
	return u, nil
}
func (a *oauthAuth) dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid OAuth destination")
	}
	ips, err := a.destinationIPs(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		c, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return c, nil
		}
	}
	return nil, fmt.Errorf("OAuth connection failed")
}

// destinationIPs applies the same policy to guarded HTTP dials and browser
// authorization destinations, which the HTTP transport does not dial itself.
func (a *oauthAuth) destinationIPs(ctx context.Context, host string) ([]net.IPAddr, error) {
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("OAuth destination lookup failed")
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("OAuth destination lookup failed")
	}
	for _, ip := range ips {
		if !publicOAuthIP(ip.IP) && !(a.local && ip.IP.IsLoopback()) {
			return nil, fmt.Errorf("OAuth destination is not public")
		}
	}
	return ips, nil
}

// do replays only a rejected request, never an ambiguous network failure. A
// snapshot of the rejected token lets the locked store detect another session's
// refresh instead of spending its newly rotated refresh token a second time.
func (t *httpTransport) do(req *http.Request) (*http.Response, error) {
	a := t.oauth
	if a == nil {
		return t.client.Do(req)
	}
	a.mu.Lock()
	token, expires := a.access, a.expires
	a.mu.Unlock()
	if token != "" && !expires.IsZero() && !time.Now().Add(15*time.Second).Before(expires) {
		if err := a.ensure(req.Context(), nil, token, false); err != nil {
			return nil, err
		}
		a.mu.Lock()
		token = a.access
		a.mu.Unlock()
	}
	// A cold connection first learns the issuer from an anonymous 401. It may
	// try the bound cache once, then refresh once if that cache was revoked.
	// Established connections permit only one 401 recovery/replay.
	recoveries := 1
	if token == "" {
		recoveries = 2
	}
	for attempt := 0; ; attempt++ {
		client := t.client
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
			client = a.resourceClient()
		}
		resp, err := client.Do(req)
		if err != nil {
			if req.Context().Err() != nil {
				return nil, req.Context().Err()
			}
			return nil, fmt.Errorf("MCP HTTP request failed")
		}
		if resp.StatusCode != http.StatusUnauthorized || attempt == recoveries {
			return resp, nil
		}
		challenge := append([]string(nil), resp.Header.Values("WWW-Authenticate")...)
		resp.Body.Close()
		if err := a.ensure(req.Context(), challenge, token, false); err != nil {
			return nil, err
		}
		retry := req.Clone(req.Context())
		if req.GetBody != nil {
			retry.Body, err = req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("cannot replay MCP authorization request")
			}
		}
		a.mu.Lock()
		token = a.access
		a.mu.Unlock()
		req = retry
	}
}

func (a *oauthAuth) resourceClient() *http.Client {
	return &http.Client{Transport: a.client.Transport, CheckRedirect: a.client.CheckRedirect}
}

func (a *oauthAuth) authorize(ctx context.Context, challenge []string, rejected string) error {
	return a.ensure(ctx, challenge, rejected, true)
}
func (a *oauthAuth) ensure(ctx context.Context, challenge []string, rejected string, browser bool) error {
	select {
	case a.gate <- struct{}{}:
		defer func() { <-a.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.metadata == nil {
		if err := a.discover(ctx, challenge); err != nil {
			return err
		}
	}
	if a.options.StoreDir == "" {
		return fmt.Errorf("MCP OAuth credential store is required")
	}
	return withOAuthCredential(ctx, a.options.StoreDir, a.endpoint, a.metadata.Issuer, a.resource, func(c *oauthCredential) error {
		if (c.AccessToken != "" && !validOAuthToken(c.AccessToken)) || (c.RefreshToken != "" && !validOAuthToken(c.RefreshToken)) || (c.ClientID != "" && !validOAuthToken(c.ClientID)) {
			return fmt.Errorf("invalid MCP OAuth cached credential")
		}
		valid := c.AccessToken != "" && (c.ExpiresAt.IsZero() || time.Now().Add(15*time.Second).Before(c.ExpiresAt))
		if valid && (rejected == "" || c.AccessToken != rejected) {
			a.mu.Lock()
			a.access = c.AccessToken
			a.expires = c.ExpiresAt
			a.mu.Unlock()
			return nil
		}
		if c.RefreshToken != "" && c.ClientID != "" {
			err := a.token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {c.RefreshToken}, "client_id": {c.ClientID}, "resource": {a.resource}}, c)
			if err == nil {
				a.mu.Lock()
				a.access = c.AccessToken
				a.expires = c.ExpiresAt
				a.mu.Unlock()
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// A rejected refresh requires deliberate reauthorization, never an
			// unsolicited browser launch during an ordinary tool call.
			if !browser {
				return &oauthRequired{a, challenge, c.AccessToken}
			}
		}
		if !browser || a.options.OpenBrowser == nil {
			return &oauthRequired{a, challenge, c.AccessToken}
		}
		if err := a.login(ctx, c); err != nil {
			return err
		}
		a.mu.Lock()
		a.access = c.AccessToken
		a.expires = c.ExpiresAt
		a.mu.Unlock()
		return nil
	})
}

const oauthJSONLimit = 1 << 20

func (a *oauthAuth) jsonRequest(ctx context.Context, method, endpoint, contentType string, body io.Reader, out any) error {
	if _, err := a.validateURL(endpoint); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("invalid OAuth request")
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("MCP OAuth request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("MCP OAuth endpoint rejected request (HTTP %d)", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, oauthJSONLimit+1))
	if err != nil || len(b) > oauthJSONLimit || json.Unmarshal(b, out) != nil {
		return fmt.Errorf("invalid or oversized MCP OAuth JSON response")
	}
	return nil
}

// bearerParameters parses comma-separated quoted HTTP auth parameters without
// confusing commas inside quoted values or parameters of another challenge.
func bearerParameters(headers []string) map[string]string {
	for _, header := range headers {
		if len(header) > 16384 {
			continue
		}
		var pieces []string
		start := 0
		quoted, escape := false, false
		for i, c := range header {
			if escape {
				escape = false
				continue
			}
			if quoted && c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				quoted = !quoted
			}
			if c == ',' && !quoted {
				pieces = append(pieces, header[start:i])
				start = i + 1
			}
		}
		pieces = append(pieces, header[start:])
		active := false
		attrs := map[string]string{}
		for _, piece := range pieces {
			piece = strings.TrimSpace(piece)
			if i := strings.IndexAny(piece, " \t"); i >= 0 && !strings.Contains(piece[:i], "=") {
				scheme := piece[:i]
				if active {
					return attrs
				}
				active = strings.EqualFold(scheme, "Bearer")
				piece = strings.TrimSpace(piece[i+1:])
			} else if strings.EqualFold(piece, "Bearer") {
				active = true
				continue
			}
			if !active {
				continue
			}
			key, value, ok := strings.Cut(piece, "=")
			if !ok {
				continue
			}
			key = strings.ToLower(strings.TrimSpace(key))
			value = strings.TrimSpace(value)
			if strings.HasPrefix(value, "\"") {
				if len(value) < 2 || !strings.HasSuffix(value, "\"") {
					continue
				}
				value = value[1 : len(value)-1]
				value = strings.ReplaceAll(strings.ReplaceAll(value, `\"`, `"`), `\\`, `\`)
			}
			if _, exists := attrs[key]; exists {
				return map[string]string{}
			}
			attrs[key] = value
		}
		if active {
			return attrs
		}
	}
	return nil
}

func wellKnown(issuer, kind string, appendPath bool) string {
	u, _ := url.Parse(issuer)
	path := strings.TrimSuffix(u.Path, "/")
	if appendPath {
		u.Path = strings.TrimSuffix(u.Path, "/") + "/.well-known/" + kind
		u.RawPath = ""
	} else {
		u.Path = "/.well-known/" + kind + path
		u.RawPath = ""
	}
	u.RawQuery = ""
	return u.String()
}
func (a *oauthAuth) discover(ctx context.Context, challenge []string) error {
	endpoint, err := a.validateURL(a.endpoint)
	if err != nil {
		return err
	}
	attrs := bearerParameters(challenge)
	candidates := []string{}
	if location := attrs["resource_metadata"]; location != "" {
		candidates = append(candidates, location)
	} else {
		candidates = append(candidates, wellKnown(a.endpoint, "oauth-protected-resource", false))
		if endpoint.Path != "" && endpoint.Path != "/" {
			root := *endpoint
			root.Path = ""
			root.RawQuery = ""
			candidates = append(candidates, wellKnown(root.String(), "oauth-protected-resource", false))
		}
	}
	type resourceMetadata struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
		Scopes               []string `json:"scopes_supported"`
	}
	var resource resourceMetadata
	found := false
	for _, candidate := range candidates {
		var candidateResource resourceMetadata
		if _, err := a.validateURL(candidate); err != nil {
			return err
		}
		if err := a.jsonRequest(ctx, http.MethodGet, candidate, "", nil, &candidateResource); err == nil {
			resource = candidateResource
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("MCP OAuth protected-resource discovery failed")
	}
	// Exact resource binding intentionally refuses sibling/path-prefix resources.
	// Query parameters are endpoint configuration, not resource identifiers.
	expected := *endpoint
	expected.RawQuery = ""
	if resource.Resource != expected.String() {
		return fmt.Errorf("MCP OAuth resource binding mismatch")
	}
	if len(resource.AuthorizationServers) == 0 || len(resource.AuthorizationServers) > 16 {
		return fmt.Errorf("MCP OAuth authorization server metadata is missing")
	}
	issuer := resource.AuthorizationServers[0]
	issuerURL, err := a.validateURL(issuer)
	if err != nil {
		return err
	}
	if issuerURL.RawQuery != "" {
		return fmt.Errorf("invalid MCP OAuth issuer")
	}
	var metadata oauthMetadata
	locations := []string{wellKnown(issuer, "oauth-authorization-server", false), wellKnown(issuer, "openid-configuration", false), wellKnown(issuer, "openid-configuration", true)}
	found = false
	for _, location := range locations {
		var candidateMetadata oauthMetadata
		if err := a.jsonRequest(ctx, http.MethodGet, location, "", nil, &candidateMetadata); err == nil {
			metadata = candidateMetadata
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("MCP OAuth authorization-server discovery failed")
	}
	if metadata.Issuer != issuer {
		return fmt.Errorf("MCP OAuth issuer binding mismatch")
	}
	for _, raw := range []string{metadata.AuthorizationEndpoint, metadata.TokenEndpoint} {
		if _, err := a.validateURL(raw); err != nil {
			return err
		}
	}
	if !slices.Contains(metadata.CodeChallengeMethods, "S256") {
		return fmt.Errorf("MCP OAuth authorization server must support S256 PKCE")
	}
	if len(metadata.TokenAuthMethods) > 0 && !slices.Contains(metadata.TokenAuthMethods, "none") {
		return fmt.Errorf("MCP OAuth requires public-client token authentication")
	}
	if len(metadata.ResponseTypes) > 0 && !slices.Contains(metadata.ResponseTypes, "code") {
		return fmt.Errorf("MCP OAuth authorization-code flow is unsupported")
	}
	scope := attrs["scope"]
	if scope == "" {
		scope = strings.Join(resource.Scopes, " ")
	}
	if len(scope) > 4096 {
		return fmt.Errorf("MCP OAuth scope exceeds size limit")
	}
	// Browsers do not use the guarded transport: reject nonpublic DNS answers
	// before registration or browser launch too. Subsequent browser DNS rebinding
	// cannot be controlled by this client.
	authorizationURL, _ := url.Parse(metadata.AuthorizationEndpoint)
	if _, err := a.destinationIPs(ctx, authorizationURL.Hostname()); err != nil {
		return err
	}
	a.metadata = &metadata
	a.resource = resource.Resource
	a.scope = scope
	return nil
}

func oauthRandom() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("OAuth randomness unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func validOAuthToken(token string) bool {
	return token != "" && len(token) <= 65536 && strings.IndexFunc(token, func(r rune) bool { return r <= 0x20 || r >= 0x7f }) < 0
}
func (a *oauthAuth) token(ctx context.Context, values url.Values, c *oauthCredential) error {
	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := a.jsonRequest(ctx, http.MethodPost, a.metadata.TokenEndpoint, "application/x-www-form-urlencoded", strings.NewReader(values.Encode()), &result); err != nil {
		return err
	}
	if !strings.EqualFold(result.TokenType, "Bearer") || !validOAuthToken(result.AccessToken) || (result.RefreshToken != "" && !validOAuthToken(result.RefreshToken)) || result.ExpiresIn < 0 || result.ExpiresIn > 315360000 {
		return fmt.Errorf("invalid MCP OAuth token response")
	}
	c.AccessToken = result.AccessToken
	if result.RefreshToken != "" {
		c.RefreshToken = result.RefreshToken
	}
	c.ExpiresAt = time.Time{}
	if result.ExpiresIn > 0 {
		c.ExpiresAt = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	}
	return nil
}

func (a *oauthAuth) login(ctx context.Context, c *oauthCredential) error {
	if a.metadata.RegistrationEndpoint == "" {
		return fmt.Errorf("MCP OAuth server does not support dynamic public-client registration")
	}
	if _, err := a.validateURL(a.metadata.RegistrationEndpoint); err != nil {
		return err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("cannot listen for MCP OAuth callback")
	}
	defer listener.Close()
	state, err := oauthRandom()
	if err != nil {
		return err
	}
	verifier, err := oauthRandom()
	if err != nil {
		return err
	}
	callbackPath, err := oauthRandom()
	if err != nil {
		return err
	}
	redirect := "http://" + listener.Addr().String() + "/callback/" + callbackPath
	// Each registration binds this exact random loopback URI to this login.
	registration, _ := json.Marshal(map[string]any{"client_name": "kh", "redirect_uris": []string{redirect}, "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}, "token_endpoint_auth_method": "none", "application_type": "native"})
	var registered struct {
		ClientID   string `json:"client_id"`
		AuthMethod string `json:"token_endpoint_auth_method"`
	}
	if err := a.jsonRequest(ctx, http.MethodPost, a.metadata.RegistrationEndpoint, "application/json", strings.NewReader(string(registration)), &registered); err != nil {
		return err
	}
	if !validOAuthToken(registered.ClientID) || (registered.AuthMethod != "" && registered.AuthMethod != "none") {
		return fmt.Errorf("invalid MCP OAuth public-client registration")
	}
	hash := sha256.Sum256([]byte(verifier))
	authURL, _ := url.Parse(a.metadata.AuthorizationEndpoint)
	query := authURL.Query()
	for key, value := range map[string]string{"response_type": "code", "client_id": registered.ClientID, "redirect_uri": redirect, "state": state, "code_challenge": base64.RawURLEncoding.EncodeToString(hash[:]), "code_challenge_method": "S256", "resource": a.resource} {
		query.Set(key, value)
	}
	if a.scope != "" {
		query.Set("scope", a.scope)
	}
	authURL.RawQuery = query.Encode()
	type callbackResult struct {
		code string
		err  error
	}
	received := make(chan callbackResult, 1)
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 16 << 10, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if r.Method != "GET" || r.URL.Path != "/callback/"+callbackPath || r.Host != listener.Addr().String() {
			http.NotFound(w, r)
			return
		}
		q, err := url.ParseQuery(r.URL.RawQuery)
		bad := err != nil
		for _, values := range q {
			if len(values) != 1 {
				bad = true
			}
		}
		if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
			bad = true
		}
		iss := q.Get("iss")
		if (q.Has("iss") && iss != a.metadata.Issuer) || (a.metadata.IssParameterSupported && !q.Has("iss")) {
			bad = true
		}
		result := callbackResult{code: q.Get("code")}
		if bad || result.code == "" || q.Get("error") != "" {
			result.err = fmt.Errorf("MCP OAuth callback validation failed")
			http.Error(w, "Authorization failed.", http.StatusBadRequest)
		} else {
			fmt.Fprintln(w, "Authorization received. You may close this window.")
		}
		select {
		case received <- result:
		default:
		}
	})}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()
	if a.options.Notice != nil {
		a.options.Notice("MCP server requires browser authorization.")
	}
	browserCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	opened := make(chan error, 1)
	go func() { opened <- a.options.OpenBrowser(browserCtx, authURL.String()) }()
	var reply callbackResult
	for {
		select {
		case reply = <-received:
			cancel()
			if reply.err != nil {
				return reply.err
			}
			c.ClientID = registered.ClientID
			c.RefreshToken = ""
			return a.token(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {reply.code}, "redirect_uri": {redirect}, "client_id": {c.ClientID}, "code_verifier": {verifier}, "resource": {a.resource}}, c)
		case err := <-opened:
			if err != nil {
				return fmt.Errorf("cannot open MCP OAuth authorization browser")
			}
			opened = nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
