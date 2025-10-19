//nolint:forbidigo,noctx,lll,gocritic,gosec,depguard
package main

// NOTE: everything below is written by AI.

import (
	"context"
	crypto_rand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// ==== EDIT THESE THREE VALUES ====.
const (
	AUTH0_DOMAIN   = "id-dev.sterndesk.com"
	AUTH0_CLIENTID = "j9kQOGUCuZnwZiT9LMSz7oTI4JlMu9OU"
	AUTH0_AUDIENCE = "basewarp-recode-api"
)

// Fixed callback (must match Auth0 Allowed Callback URLs).
const redirectURI = "http://localhost:5173/auth/login-callback"

const (
	defaultScope  = "openid"
	tokenFile     = "../insecureaccesstools/test_access_token3.txt"
	tokenJSON     = "token.json"
	serverAddress = "localhost:5173"
	callbackPath  = "/auth/login-callback"
)

func pkcePair() (verifier string, challenge string, err error) {
	b := make([]byte, 32)
	if _, err = crypto_rand.Read(b); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])
	return verifier, challenge, nil
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := crypto_rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func openInBrowser(url string) { _ = exec.Command("open", url).Start() } // macOS

func main() {
	if AUTH0_DOMAIN == "" || AUTH0_CLIENTID == "" {
		log.Fatal("Please set AUTH0_DOMAIN and AUTH0_CLIENTID constants at the top of main.go")
	}

	// 1) Prepare OAuth2 (Auth0) with fixed redirect
	endpoint := oauth2.Endpoint{
		AuthURL:  "https://" + AUTH0_DOMAIN + "/authorize",
		TokenURL: "https://" + AUTH0_DOMAIN + "/oauth/token",
	}
	oauthCfg := &oauth2.Config{
		ClientID:    AUTH0_CLIENTID,
		RedirectURL: redirectURI,
		Scopes:      strings.Split(defaultScope, " "),
		Endpoint:    endpoint,
	}

	// 2) PKCE + state
	verifier, challenge, err := pkcePair()
	if err != nil {
		log.Fatalf("failed to create PKCE pair: %v", err)
	}
	state, err := randomState()
	if err != nil {
		log.Fatalf("failed to create state: %v", err)
	}

	// 3) Build Auth URL (with PKCE + optional audience)
	var opts []oauth2.AuthCodeOption
	opts = append(opts,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		oauth2.SetAuthURLParam("prompt", "login select_account"),
		oauth2.SetAuthURLParam("response_mode", "query"),
	)
	if AUTH0_AUDIENCE != "" {
		opts = append(opts, oauth2.SetAuthURLParam("audience", AUTH0_AUDIENCE))
	}
	authURL := oauthCfg.AuthCodeURL(state, opts...)

	// 4) Start server on http://localhost:5173 and handle /auth/login-callback
	mux := http.NewServeMux()

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if errStr := q.Get("error"); errStr != "" {
			desc := q.Get("error_description")
			errCh <- fmt.Errorf("authorization error: %s (%s)", errStr, desc)
			http.Error(w, "Authorization failed. You can close this window.", http.StatusBadRequest)
			return
		}
		if q.Get("state") != state {
			errCh <- errors.New("state mismatch")
			http.Error(w, "State mismatch. You can close this window.", http.StatusBadRequest)
			return
		}
		code := q.Get("code")
		if code == "" {
			errCh <- errors.New("missing authorization code")
			http.Error(w, "Missing code. You can close this window.", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("<html><body><h2>Auth complete ✅</h2><p>You can close this window and return to the terminal.</p></body></html>"))
		codeCh <- code
	})

	// A friendly root page (optional)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body><p>This helper is running. Use your terminal to start the Auth0 flow.</p></body></html>"))
	})

	server := &http.Server{
		Addr:              serverAddress,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Verify port availability early to give a clear error
	if ln, err := net.Listen("tcp", serverAddress); err != nil {
		log.Fatalf("Port %s is in use. Close the app using it or change the code to another port.\nError: %v", serverAddress, err)
	} else {
		_ = ln.Close()
	}

	go func() {
		if serveErr := server.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
		}
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	log.Printf("Redirect URL (must be in Auth0 Allowed Callback URLs): %s", redirectURI)
	log.Printf("Opening browser for Auth0 login at: %s", endpoint.AuthURL)

	// 5) Kick off the browser login
	openInBrowser(authURL)
	fmt.Println("If the browser didn’t open, manually visit:\n", authURL)

	// 6) Wait for result (timeout 10m)
	var authorizationCode string
	select {
	case authorizationCode = <-codeCh:
	case e := <-errCh:
		log.Fatalf("authorization failed: %v", e)
	case <-time.After(10 * time.Minute):
		log.Fatal("timed out waiting for authorization (10m)")
	}

	// 7) Exchange code (with PKCE verifier)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	token, err := oauthCfg.Exchange(
		ctx,
		authorizationCode,
		oauth2.SetAuthURLParam("code_verifier", verifier),
	)
	if err != nil {
		log.Fatalf("token exchange failed: %v", err)
	}

	// 8) Persist tokens
	if err := os.WriteFile(tokenFile, []byte(token.AccessToken), 0o600); err != nil {
		log.Fatalf("failed writing %s: %v", tokenFile, err)
	}

	type tokenDump struct {
		AccessToken  string    `json:"access_token"`
		TokenType    string    `json:"token_type"`
		RefreshToken string    `json:"refresh_token,omitempty"`
		Expiry       time.Time `json:"expiry"`
		ObtainedAt   time.Time `json:"obtained_at"`
	}
	dump := tokenDump{
		AccessToken:  token.AccessToken,
		TokenType:    token.TokenType,
		RefreshToken: token.RefreshToken,
		Expiry:       token.Expiry,
		ObtainedAt:   time.Now(),
	}
	if b, marshalErr := json.MarshalIndent(dump, "", "  "); marshalErr == nil {
		if err := os.WriteFile(tokenJSON, b, 0o600); err != nil {
			log.Fatalf("failed writing %s: %v", tokenJSON, err)
		}
	}

	fmt.Println("✅ Access token saved to", tokenFile)
	fmt.Println("ℹ️  Full token details saved to", tokenJSON)
}
