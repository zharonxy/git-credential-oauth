package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/authhandler"
	"golang.org/x/oauth2/endpoints"
)

var configByHost = map[string]oauth2.Config{
	// https://github.com/settings/applications/2017944 owned by hickford
	"github.com": {ClientID: "b895675a4e2cf54d5c6c", ClientSecret: "2b746eea028711749c5062b9fe626fed78d03cc0", Endpoint: endpoints.GitHub, Scopes: []string{"repo", "gist", "workflow"}},
	// https://gitlab.com/oauth/applications/232663 owned by hickford
	"gitlab.com":             {ClientID: "10bfbbf46e5b760b55ce772a262d7a0205eacc417816eb84d37d0fb02c89bb97", ClientSecret: "e1802e0ac361efc72f8e2024e6fd5855bfdf73524b67740c05e755f55b97eb39", Endpoint: endpoints.GitLab, Scopes: []string{"read_repository", "write_repository"}},
	"gitlab.freedesktop.org": {ClientID: "ba28f287f465c03c629941bca9de965923c561f8e967ce02673a0cd937a94b6f", ClientSecret: "e3b4dba6e99a0b25cc3d3d640e418d6cc5dbeb2e2dc4c3ca791d2a22308e951c", Endpoint: replaceHost(endpoints.GitLab, "gitlab.freedesktop.org"), Scopes: []string{"read_repository", "write_repository"}},
	// https://gitlab.gnome.org/oauth/applications/112 owned by hickford
	"gitlab.gnome.org": {ClientID: "9719f147e6117ef0ee9954516bd7fe292176343a7fd24a8bcd5a686e8ef1ec71", ClientSecret: "f4e027961928ba9322fd980f5c4ee768dc7b6cb8fd7a81f959feb61b8fdec9f3", Endpoint: replaceHost(endpoints.GitLab, "gitlab.gnome.org"), Scopes: []string{"read_repository", "write_repository"}},
	"gitea.com":        {ClientID: "e13f8ebc-398d-4091-9481-5a37a76b51f6", ClientSecret: "gto_gyodepoilwdv4g2nnigosazr2git7kkko3gadqgwqa3f6dxugi6a", Endpoint: oauth2.Endpoint{AuthURL: "https://gitea.com/login/oauth/authorize", TokenURL: "https://gitea.com/login/oauth/access_token"}},
	"bitbucket.org":    {ClientID: "abET6ywGmTknNRvAMT", ClientSecret: "df8rsnkAxuHCgZrSgu5ykJQjrbGVzT9m", Endpoint: endpoints.Bitbucket, Scopes: []string{"repository", "repository:write"}},
}

var (
	verbose bool
	// populated by GoReleaser https://goreleaser.com/cookbooks/using-main.version
	version = "dev"
)

func printVersion() {
	info, ok := debug.ReadBuildInfo()
	if ok && version == "dev" {
		version = info.Main.Version
	}
	if verbose {
		fmt.Fprintf(os.Stderr, "git-credential-oauth %s\n", version)
	}
}

func parse(input string) map[string]string {
	lines := strings.Split(string(input), "\n")
	pairs := map[string]string{}
	for _, line := range lines {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) >= 2 {
			pairs[parts[0]] = parts[1]
		}
	}
	return pairs
}

func main() {
	flag.BoolVar(&verbose, "verbose", false, "log debug information to stderr")
	flag.Usage = func() {
		printVersion()
		fmt.Fprintln(os.Stderr, "usage: git credential-cache [<options>] <action>")
		flag.PrintDefaults()
		fmt.Fprintln(os.Stderr, "See also https://git-scm.com/docs/gitcredentials#_custom_helpers")
	}
	flag.Parse()
	args := flag.Args()
	if len(args) != 1 {
		flag.Usage()
		os.Exit(2)
	}
	switch args[0] {
	case "get":
		printVersion()
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			log.Fatalln(err)
		}
		pairs := parse(string(input))
		if verbose {
			fmt.Fprintln(os.Stderr, "input: ", pairs)
		}
		c, ok := configByHost[pairs["host"]]
		if !ok {
			return
		}
		state := uuid.New().String()
		queries := make(chan url.Values)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// TODO: consider whether to show errors in browser or command line
			queries <- r.URL.Query()
			w.Write([]byte("Success. You may close this page and return to Git."))
		}))
		defer server.Close()
		c.RedirectURL = server.URL
		tokenSource := authhandler.TokenSourceWithPKCE(context.Background(), &c, state, func(authCodeURL string) (code string, state string, err error) {
			defer server.Close()
			fmt.Fprintln(os.Stderr, "Please complete authentication in your browser")
			if verbose {
				fmt.Fprintln(os.Stderr, authCodeURL)
			}
			err = exec.Command("open", authCodeURL).Run()
			if err != nil {
				return "", "", err
			}
			query := <-queries
			if verbose {
				fmt.Fprintln(os.Stderr, "query:", query)
			}
			return query.Get("code"), query.Get("state"), nil
		}, generatePKCEParams())
		token, err := tokenSource.Token()
		if err != nil {
			log.Fatalln(err)
		}
		if verbose {
			fmt.Fprintln(os.Stderr, "token:", token)
		}
		fmt.Printf("username=%s\n", "oauth2")
		fmt.Printf("password=%s\n", token.AccessToken)
	}
}

func randomString(n int) (string, error) {
	data := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, data); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func replaceHost(e oauth2.Endpoint, host string) oauth2.Endpoint {
	url, err := url.Parse(e.AuthURL)
	if err != nil {
		panic(err)
	}
	e.AuthURL = strings.Replace(e.AuthURL, url.Host, host, 1)
	e.TokenURL = strings.Replace(e.TokenURL, url.Host, host, 1)
	return e
}

func generatePKCEParams() *authhandler.PKCEParams {
	verifier := uuid.New().String()
	sha := sha256.Sum256([]byte(verifier))
	challenge := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(sha[:])

	return &authhandler.PKCEParams{
		Challenge:       challenge,
		ChallengeMethod: "S256",
		Verifier:        verifier,
	}
}
