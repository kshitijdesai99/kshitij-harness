// Package auth logs in to ChatGPT for Codex, the same way Hermes does.
package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	issuer   = "https://auth.openai.com"
	clientID = "app_EMoamEEZ73f0CkXaXp7hrann" // public Codex CLI client
)

type tokens struct {
	Access  string `json:"access_token"`
	Refresh string `json:"refresh_token"`
}

// Our own file, not ~/.codex: refresh tokens are single-use, so sharing
// one with the Codex CLI would log one of us out.
func tokenPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kh", "codex.json")
}

// Login runs the device-code flow: show a code, wait for the user, save tokens.
func Login(ctx context.Context) error {
	var dc struct {
		ID   string `json:"device_auth_id"`
		Code string `json:"user_code"`
	}
	if err := post(ctx, issuer+"/api/accounts/deviceauth/usercode", map[string]string{"client_id": clientID}, &dc); err != nil {
		return err
	}
	fmt.Printf("Open %s/codex/device and enter: %s\nWaiting...\n", issuer, dc.Code)

	var got struct {
		Code     string `json:"authorization_code"`
		Verifier string `json:"code_verifier"`
	}
	for deadline := time.Now().Add(15 * time.Minute); got.Code == ""; {
		if time.Now().After(deadline) {
			return fmt.Errorf("login timed out")
		}
		time.Sleep(5 * time.Second)
		err := post(ctx, issuer+"/api/accounts/deviceauth/token", map[string]string{"device_auth_id": dc.ID, "user_code": dc.Code}, &got)
		if err != nil && !strings.Contains(err.Error(), "status 403") && !strings.Contains(err.Error(), "status 404") {
			return err // 403/404 just mean "not signed in yet"
		}
	}

	var t tokens
	err := post(ctx, issuer+"/oauth/token", url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {got.Code},
		"redirect_uri":  {issuer + "/deviceauth/callback"},
		"client_id":     {clientID},
		"code_verifier": {got.Verifier},
	}, &t)
	if err != nil {
		return err
	}
	return withLock(func(f *os.File) error { return save(f, t) })
}

// Token returns a fresh access token and the ChatGPT account ID.
func Token(ctx context.Context) (access, account string, err error) {
	err = withLock(func(f *os.File) error {
		var t tokens
		if json.NewDecoder(f).Decode(&t) != nil || t.Refresh == "" {
			return fmt.Errorf("not logged in: run `kh login codex`")
		}
		// Refresh 2 min early. We read inside the lock, so if another kh
		// already refreshed we see its new tokens and never reuse a spent one.
		if exp, _ := claims(t.Access)["exp"].(float64); time.Until(time.Unix(int64(exp), 0)) < 2*time.Minute {
			var n tokens
			err := post(ctx, issuer+"/oauth/token", url.Values{
				"grant_type":    {"refresh_token"},
				"refresh_token": {t.Refresh},
				"client_id":     {clientID},
			}, &n)
			if err != nil {
				return err
			}
			if n.Refresh == "" {
				n.Refresh = t.Refresh
			}
			if err := save(f, n); err != nil {
				return err
			}
			t = n
		}
		access = t.Access
		auth, _ := claims(t.Access)["https://api.openai.com/auth"].(map[string]any)
		account, _ = auth["chatgpt_account_id"].(string)
		return nil
	})
	return access, account, err
}

// withLock opens the token file (0600) under an exclusive lock.
func withLock(fn func(*os.File) error) error {
	os.MkdirAll(filepath.Dir(tokenPath()), 0o700)
	f, err := os.OpenFile(tokenPath(), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	return fn(f)
}

func save(f *os.File, t tokens) error {
	f.Truncate(0)
	f.Seek(0, 0)
	return json.NewEncoder(f).Encode(t)
}

// claims reads a JWT payload without verifying it; we only need our own fields.
func claims(jwt string) map[string]any {
	m := map[string]any{}
	if parts := strings.Split(jwt, "."); len(parts) == 3 {
		b, _ := base64.RawURLEncoding.DecodeString(parts[1])
		json.Unmarshal(b, &m)
	}
	return m
}

// post sends JSON, or a form when body is url.Values, and decodes the reply.
func post(ctx context.Context, u string, body any, out any) error {
	var r io.Reader
	ct := "application/json"
	if form, ok := body.(url.Values); ok {
		r, ct = strings.NewReader(form.Encode()), "application/x-www-form-urlencoded"
	} else {
		b, _ := json.Marshal(body)
		r = strings.NewReader(string(b))
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", u, r)
	req.Header.Set("Content-Type", ct)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s: status %d: %s", u, resp.StatusCode, b)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
